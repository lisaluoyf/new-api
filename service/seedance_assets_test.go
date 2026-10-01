package service

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func seedanceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	oldDB, oldMemory := model.DB, common.MemoryCacheEnabled
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.SeedanceResource{}, &model.Channel{}, &model.Ability{}))
	model.DB, common.MemoryCacheEnabled = db, true
	t.Cleanup(func() {
		model.DB, common.MemoryCacheEnabled = oldDB, oldMemory
		if oldDB != nil && oldMemory {
			model.InitChannelCache()
		}
	})
	return db
}

func seedanceContext(user int) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/seedance2/private-avatar/assets", nil)
	c.Set("id", user)
	common.SetContextKey(c, constant.ContextKeyTokenGroup, "default")
	return c
}

func TestSeedanceLibraryPartialReviewMappingAndIsolation(t *testing.T) {
	db := seedanceTestDB(t)
	key := "sk-provider-secret-test"
	var submitted []SeedanceAssetInput
	upstreamCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		require.Equal(t, "Bearer "+key, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case seedanceLibraryPath + "/assets":
			var request struct {
				Assets  []SeedanceAssetInput `json:"assets"`
				GroupID string               `json:"group_id"`
			}
			require.NoError(t, common.DecodeJson(r.Body, &request))
			require.Equal(t, "provider-group", request.GroupID)
			submitted = request.Assets
			fmt.Fprint(w, `{"code":200,"data":{"id":"provider-review","status":"processing"},"cost":5,"raw_response":{"secret":"private"}}`)
		case "/v1/tasks/provider-review":
			// Reversed completion order must not attach the failed item to
			// the first customer's successful asset.
			fmt.Fprintf(w, `{"code":200,"data":{"status":"failed","progress":100,"cost":8,"credits_cost":99,"result":{"assets":[{"asset_id":"provider-b","status":"Failed","raw_response":{"Result":{"Name":%q,"URL":"https://provider.example/private","secret":%q}}},{"asset_id":"provider-a","status":"Active","raw_response":{"Result":{"Name":%q}}}]},"error":{"message":"provider-b rejected; api_key=%s"}}}`, submitted[1].Name, key, submitted[0].Name, key)
		default:
			t.Errorf("unexpected provider request %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	base := server.URL
	require.NoError(t, db.Create(&model.Channel{Id: 7, Key: key, BaseURL: &base, Status: 1, Models: "seedance-2.5", Group: "default"}).Error)
	require.NoError(t, db.Create(&model.Ability{ChannelId: 7, Model: "seedance-2.5", Group: "default", Enabled: true}).Error)
	model.InitChannelCache()
	group := model.SeedanceResource{ID: "group_public", Kind: "group", UserID: 1, ChannelID: 7, KeyFingerprint: SeedanceKeyFingerprint(key), UpstreamID: "provider-group", Status: "Active"}
	require.NoError(t, db.Create(&group).Error)
	c := seedanceContext(1)
	task, err := SubmitSeedanceAssets(c, SeedanceAssetSubmission{Model: "seedance-2.5", GroupID: group.ID, AssetType: "Image", Assets: []SeedanceAssetInput{{URL: "https://example.com/a.png", Name: "A"}, {URL: "https://example.com/b.png", Name: "B"}}})
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(task.ID, "asset_task_"))
	require.NotEqual(t, "provider-review", task.ID)
	var ids []string
	require.NoError(t, common.UnmarshalJsonStr(task.RequestData, &ids))
	require.Len(t, ids, 2)
	require.Equal(t, ids[0], submitted[0].Name)
	require.NoError(t, PollSeedanceAssetTask(c, task))
	require.Equal(t, "failed", task.Status)
	a, err := model.GetSeedanceResource(1, "asset", ids[0])
	require.NoError(t, err)
	b, err := model.GetSeedanceResource(1, "asset", ids[1])
	require.NoError(t, err)
	require.Equal(t, "Active", a.Status)
	require.Equal(t, "provider-a", a.UpstreamID)
	require.Equal(t, "A", a.Name)
	require.Equal(t, "Failed", b.Status)
	require.Equal(t, "provider-b", b.UpstreamID)
	public, err := common.Marshal(SeedanceTaskDTO(task))
	require.NoError(t, err)
	for _, private := range []string{key, "provider-review", "provider-a", "provider-b", "provider-group", "provider.example", "raw_response", "credits_cost", "\"cost\"", "channel_id", "key_fingerprint"} {
		require.NotContains(t, string(public), private)
	}
	require.Contains(t, string(public), "usable_assets")
	require.Contains(t, string(public), "failed_assets")
	require.Contains(t, string(public), "asset://"+a.ID)
	fields := map[string]any{"model": "seedance-2.5", "image_with_roles": []any{map[string]any{"url": "asset://" + a.ID, "role": "first_frame"}}, "generate_audio": false, "seed": float64(0)}
	pin, err := SeedanceAssetRouting(c, fields)
	require.NoError(t, err)
	require.Equal(t, 7, pin.ChannelID)
	resolved, err := ResolveSeedanceAssetReferences(c, fields, 7, key)
	require.NoError(t, err)
	require.Equal(t, false, resolved["generate_audio"])
	require.Equal(t, float64(0), resolved["seed"])
	original, _ := common.Marshal(fields)
	forwarded, _ := common.Marshal(resolved)
	require.Contains(t, string(original), "asset://"+a.ID)
	require.NotContains(t, string(original), "provider-a")
	require.Contains(t, string(forwarded), "asset://provider-a")
	_, err = SeedanceAssetRouting(seedanceContext(2), fields)
	require.ErrorContains(t, err, "not found")
	_, err = ResolveSeedanceAssetReferences(c, fields, 8, key)
	require.Error(t, err)
	_, err = ResolveSeedanceAssetReferences(c, fields, 7, "another-provider-key")
	require.Error(t, err)
	_, err = SubmitSeedanceAssets(seedanceContext(2), SeedanceAssetSubmission{Model: "seedance-2.5", GroupID: group.ID, Assets: []SeedanceAssetInput{{URL: "https://example.com/foreign.png"}}})
	require.ErrorContains(t, err, "not found")
	require.Equal(t, 2, upstreamCalls)
	// Asset submission/review require no billing tables or quota ledger writes.
	require.False(t, db.Migrator().HasTable("logs"))
	require.False(t, db.Migrator().HasTable("tasks"))
	common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, true)
	common.SetContextKey(c, constant.ContextKeyTokenModelLimit, map[string]bool{"gpt-5": true})
	_, err = SeedanceAssetRouting(c, fields)
	require.ErrorContains(t, err, "cannot access")
}

func TestSeedanceAssetInputValidationAndPrivateDTO(t *testing.T) {
	for _, raw := range []string{"file:///tmp/test.png", "asset://foreign", "https://localhost/a.png", "http://127.0.0.1/a.png", "https://user:password@example.com/a.png", "http://0.0.0.0/a.png"} {
		input := SeedanceAssetSubmission{Model: "seedance-2.5", Assets: []SeedanceAssetInput{{URL: raw}}}
		require.Error(t, ValidateSeedanceAssetSubmission(&input), raw)
	}
	input := SeedanceAssetSubmission{Model: "seedance-2.5", Assets: make([]SeedanceAssetInput, 21)}
	require.ErrorContains(t, ValidateSeedanceAssetSubmission(&input), "20")
	resource := &model.SeedanceResource{ID: "asset_public", Kind: "asset", UserID: 999, ChannelID: 7, UpstreamID: "provider-secret-id", KeyFingerprint: "secret-fingerprint", Status: "Active", RequestData: "private-request"}
	for _, dto := range []any{SeedanceAssetDTO(resource), SeedanceGroupDTO(resource), resource} {
		raw, err := common.Marshal(dto)
		require.NoError(t, err)
		for _, private := range []string{"999", "channel_id", "provider-secret-id", "secret-fingerprint", "private-request"} {
			require.NotContains(t, string(raw), private)
		}
	}
}

func TestSeedanceProviderErrorsAndRedirectsDoNotExposeCredentials(t *testing.T) {
	db := seedanceTestDB(t)
	key := "sk-private-provider-key-long"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			w.Header().Set("Location", "https://provider.example/secret")
			w.WriteHeader(302)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		fmt.Fprintf(w, `{"error":{"message":"Invalid media ratio; group=provider-group-id; api_key=%s; https://provider.example/private/provider-id"},"secret":%q}`, key, key)
	}))
	defer server.Close()
	base := server.URL
	require.NoError(t, db.Create(&model.Channel{Id: 7, Status: 1, Key: key, BaseURL: &base}).Error)
	r := &model.SeedanceResource{ChannelID: 7, KeyFingerprint: SeedanceKeyFingerprint(key), UpstreamID: "provider-id"}
	_, err := seedanceProviderRequest(seedanceContext(1).Request.Context(), r, http.MethodPost, "/error", map[string]any{"group_id": "provider-group-id"})
	require.ErrorContains(t, err, "Invalid media ratio")
	for _, secret := range []string{key, "provider.example", "provider-id", "provider-group-id"} {
		require.NotContains(t, err.Error(), secret)
	}
	_, err = seedanceProviderRequest(seedanceContext(1).Request.Context(), r, http.MethodGet, "/redirect", nil)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "provider.example")
}

func TestUploadedImageStaysOnAPIMasterAndRejectsNonImages(t *testing.T) {
	oldDir, oldBase := imageCacheDir, imageCachePublicBase
	imageCacheDir, imageCachePublicBase = t.TempDir(), "https://apimaster.ai/imgs/"
	t.Cleanup(func() { imageCacheDir, imageCachePublicBase = oldDir, oldBase })
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 640, 640))))
	url, err := StoreUploadedMediaImage(encoded.Bytes(), "image/png")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(url, imageCachePublicBase+"media_upload_"))
	saved, err := os.ReadFile(filepath.Join(imageCacheDir, strings.TrimPrefix(url, imageCachePublicBase)))
	require.NoError(t, err)
	require.Equal(t, encoded.Bytes(), saved)
	_, err = StoreUploadedMediaImage([]byte("<html>login</html>"), "image/png")
	require.Error(t, err)
	_, err = StoreUploadedMediaImage(encoded.Bytes(), "application/pdf")
	require.Error(t, err)
}

func TestSeedanceVideoFailureRedactsDeletedPrivateAssets(t *testing.T) {
	db := seedanceTestDB(t)
	asset := model.SeedanceResource{ID: "asset_public", UserID: 1, Kind: "asset", UpstreamID: "private-asset-identifier"}
	require.NoError(t, db.Create(&asset).Error)
	require.NoError(t, db.Delete(&asset).Error)
	task := model.Task{UserId: 1, Status: model.TaskStatusFailure, FailReason: "Reference private-asset-identifier is unavailable; asset://private-reference; group_id=private-group", PrivateData: model.TaskPrivateData{RequestData: `{"image_urls":["asset://asset_public"]}`}}
	reason := PublicTaskFailure(&task)
	require.Contains(t, reason, "unavailable")
	for _, secret := range []string{"private-asset-identifier", "private-reference", "private-group"} {
		require.NotContains(t, reason, secret)
	}
	require.Contains(t, task.FailReason, "private-asset-identifier")
}
