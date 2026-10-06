package service

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func bytePlusTestLibrary(t *testing.T, handler http.HandlerFunc) *gorm.DB {
	t.Helper()
	db := seedanceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.ChannelModelPricing{}))
	dir := t.TempDir()
	t.Setenv("BYTEPLUS_ASSET_CREDENTIALS_DIR", dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "channel-283.json"), []byte(`{"access_key_id":"test-ak","secret_access_key":"test-sk","project_name":"test-project"}`), 0600))
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	original := bytePlusAssetEndpoint
	bytePlusAssetEndpoint = func(region string) string { require.Equal(t, "ap-southeast-1", region); return server.URL + "/" }
	t.Cleanup(func() { bytePlusAssetEndpoint = original })
	base := "https://ark.ap-southeast.bytepluses.com"
	setting := `{"manual_group_ratio":1}`
	require.NoError(t, db.Create(&model.Channel{Id: 283, Setting: &setting, Type: constant.ChannelTypeDoubaoVideo, Key: "inference-key", BaseURL: &base, Status: 1, Models: "seedance-2.5", Group: "default"}).Error)
	require.NoError(t, db.Create(&model.Ability{ChannelId: 283, Model: "seedance-2.5", Group: "default", Enabled: true}).Error)
	model.InitChannelCache()
	return db
}
func bytePlusTestRequest(t *testing.T, r *http.Request) (string, map[string]any) {
	t.Helper()
	require.Equal(t, "POST", r.Method)
	require.Equal(t, "2024-01-01", r.URL.Query().Get("Version"))
	require.Contains(t, r.Header.Get("Authorization"), "HMAC-SHA256 Credential=test-ak/")
	require.Contains(t, r.Header.Get("Authorization"), "/ap-southeast-1/ark/request")
	require.Contains(t, r.Header.Get("Authorization"), "SignedHeaders=content-type;host;x-content-sha256;x-date")
	require.NotContains(t, r.Header.Get("Authorization"), "inference-key")
	raw, err := io.ReadAll(r.Body)
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf("%x", sha256.Sum256(raw)), r.Header.Get("X-Content-Sha256"))
	var fields map[string]any
	require.NoError(t, common.Unmarshal(raw, &fields))
	require.Equal(t, "test-project", fields["ProjectName"])
	require.NotContains(t, fields, "Moderation")
	return r.URL.Query().Get("Action"), fields
}
func TestBytePlusLibraryBatchReviewRoutingAndCRUD(t *testing.T) {
	phase, imported := 0, 0
	calls := []string{}
	db := bytePlusTestLibrary(t, func(w http.ResponseWriter, r *http.Request) {
		action, fields := bytePlusTestRequest(t, r)
		calls = append(calls, action)
		switch action {
		case "CreateAssetGroup":
			require.Equal(t, "AIGC", fields["GroupType"])
			fmt.Fprint(w, `{"Result":{"Id":"private-group"}}`)
		case "CreateAsset":
			require.Equal(t, "private-group", fields["GroupId"])
			require.Equal(t, "Image", fields["AssetType"])
			require.True(t, strings.HasPrefix(fields["Name"].(string), "asset_"))
			imported++
			fmt.Fprintf(w, `{"Result":{"Id":"private-%d"}}`, imported)
		case "GetAsset":
			status := "Processing"
			if fields["Id"] == "private-1" {
				status = "Active"
			} else if phase > 0 {
				status = "Failed"
			}
			fmt.Fprintf(w, `{"Result":{"Status":%q,"URL":"https://private.example/signed"}}`, status)
		case "UpdateAsset":
			require.Equal(t, "Renamed", fields["Name"])
			fmt.Fprint(w, `{"Result":{}}`)
		case "UpdateAssetGroup":
			require.Equal(t, "", fields["Description"])
			fmt.Fprint(w, `{"Result":{}}`)
		case "DeleteAsset", "DeleteAssetGroup":
			fmt.Fprint(w, `{"Result":{}}`)
		default:
			t.Errorf("unexpected %s", action)
			w.WriteHeader(500)
		}
	})
	c := seedanceContext(1)
	task, err := SubmitSeedanceAssets(c, SeedanceAssetSubmission{Model: "seedance-2.5", Group: &SeedanceGroupInput{Name: "Characters"}, Assets: []SeedanceAssetInput{{URL: "https://example.com/a.jpg", Name: "A"}, {URL: "https://example.com/b.jpg", Name: "B"}}})
	require.NoError(t, err)
	require.Equal(t, 283, task.ChannelID)
	require.NoError(t, PollSeedanceAssetTask(c, task))
	require.Equal(t, "processing", task.Status)
	require.Equal(t, 50, task.Progress)
	phase = 1
	require.NoError(t, PollSeedanceAssetTask(c, task))
	require.Equal(t, "failed", task.Status)
	var assets []model.SeedanceResource
	require.NoError(t, db.Where("group_id = ? AND kind = ?", task.GroupID, "asset").Order("upstream_id").Find(&assets).Error)
	require.Len(t, assets, 2)
	require.Equal(t, "Active", assets[0].Status)
	require.Equal(t, "Failed", assets[1].Status)
	public, err := common.Marshal(SeedanceTaskDTO(task))
	require.NoError(t, err)
	for _, secret := range []string{"private-", "test-ak", "test-sk", "inference-key", "private.example", "channel_id"} {
		require.NotContains(t, string(public), secret)
	}
	fields := map[string]any{"model": "seedance-2.5", "image_urls": []any{"asset://" + assets[0].ID}, "generate_audio": false, "seed": float64(0)}
	pin, err := SeedanceAssetRouting(c, fields)
	require.NoError(t, err)
	require.Equal(t, 283, pin.ChannelID)
	resolved, err := ResolveSeedanceAssetReferences(c, fields, 283, "inference-key")
	require.NoError(t, err)
	require.Equal(t, []any{"asset://private-1"}, resolved["image_urls"])
	require.Equal(t, false, resolved["generate_audio"])
	require.Equal(t, float64(0), resolved["seed"])
	_, err = SeedanceAssetRouting(seedanceContext(2), fields)
	require.ErrorContains(t, err, "not found")
	require.NoError(t, RefreshSeedanceAsset(c, &assets[0]))
	name := "Renamed"
	require.NoError(t, UpdateSeedanceResource(c, &assets[0], &name, nil))
	group, err := model.GetSeedanceResource(1, "group", task.GroupID)
	require.NoError(t, err)
	empty := ""
	require.NoError(t, UpdateSeedanceResource(c, group, nil, &empty))
	require.NoError(t, DeleteSeedanceResource(c, &assets[0]))
	require.NoError(t, DeleteSeedanceResource(c, &assets[1]))
	require.NoError(t, DeleteSeedanceResource(c, group))
	require.Contains(t, calls, "DeleteAssetGroup")
	require.False(t, db.Migrator().HasTable("logs"))
}
func TestBytePlusPortraitVerificationCreatesOwnedGroup(t *testing.T) {
	verified := false
	calls := 0
	db := bytePlusTestLibrary(t, func(w http.ResponseWriter, r *http.Request) {
		action, fields := bytePlusTestRequest(t, r)
		calls++
		switch action {
		case "CreateVisualValidateSession":
			require.Equal(t, "https://example.com/done", fields["CallbackURL"])
			fmt.Fprint(w, `{"Result":{"BytedToken":"private-validation-token","H5Link":"https://www.byteplus.com/en/liveness-face-manage/authorization?pl=ephemeral"}}`)
		case "GetVisualValidateResult":
			require.Equal(t, "private-validation-token", fields["BytedToken"])
			if verified {
				fmt.Fprint(w, `{"Result":{"GroupId":"real-portrait-group"}}`)
			} else {
				fmt.Fprint(w, `{"Result":{}}`)
			}
		case "CreateAsset":
			require.Equal(t, "real-portrait-group", fields["GroupId"])
			fmt.Fprint(w, `{"Result":{"Id":"real-portrait-asset"}}`)
		default:
			t.Errorf("unexpected %s", action)
			w.WriteHeader(500)
		}
	})
	c := seedanceContext(1)
	v, err := CreateSeedanceVerification(c, SeedanceVerificationInput{Model: "seedance-2.5", CallbackURL: "https://example.com/done"})
	require.NoError(t, err)
	require.NoError(t, RefreshSeedanceVerification(c, v))
	require.Equal(t, "pending", v.Status)
	var count int64
	require.NoError(t, db.Model(&model.SeedanceResource{}).Where("kind = ?", "group").Count(&count).Error)
	require.Zero(t, count)
	verified = true
	require.NoError(t, RefreshSeedanceVerification(c, v))
	require.Equal(t, "completed", v.Status)
	require.NoError(t, RefreshSeedanceVerification(c, v))
	require.Equal(t, 3, calls)
	dto, err := common.Marshal(SeedanceVerificationDTO(v))
	require.NoError(t, err)
	require.NotContains(t, string(dto), "private-validation-token")
	require.NotContains(t, string(dto), "real-portrait-group")
	require.NotContains(t, string(dto), "verification_url")
	group, err := model.GetSeedanceResource(1, "group", v.GroupID)
	require.NoError(t, err)
	require.Equal(t, 283, group.ChannelID)
	require.Equal(t, "real-portrait-group", group.UpstreamID)
	_, err = model.GetSeedanceResource(2, "verification", v.ID)
	require.Error(t, err)
	_, err = SubmitSeedanceAssets(c, SeedanceAssetSubmission{Model: "seedance-2.5", GroupID: group.ID, Assets: []SeedanceAssetInput{{URL: "https://example.com/portrait.jpg"}}})
	require.NoError(t, err)
	_, err = CreateSeedanceVerification(c, SeedanceVerificationInput{CallbackURL: "http://127.0.0.1/done"})
	require.Error(t, err)
}
func TestBytePlusCredentialsErrorsAndRedirects(t *testing.T) {
	calls := 0
	db := bytePlusTestLibrary(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("Action") == "CreateAssetGroup" {
			fmt.Fprint(w, `{"ResponseMetadata":{"Error":{"Code":"SubscriptionRequired","Message":"private subscription message"}}}`)
			return
		}
		if r.URL.Query().Get("Action") == "GetAsset" {
			w.Header().Set("Location", "https://foreign.example/")
			w.WriteHeader(302)
			return
		}
		fmt.Fprint(w, `{"ResponseMetadata":{"Error":{"Message":"Denied test-ak test-sk private-id https://private.example/token"}}}`)
	})
	ch, err := model.GetChannelById(283, true)
	require.NoError(t, err)
	require.True(t, isBytePlusSeedanceChannel(ch))
	foreign := "https://ark.ap-southeast.bytepluses.com.attacker.example"
	require.False(t, isBytePlusSeedanceChannel(&model.Channel{Type: 54, BaseURL: &foreign}))
	resource := &model.SeedanceResource{ChannelID: 283, Kind: "asset", UpstreamID: "private-id"}
	_, err = bytePlusAssetRequest(seedanceContext(1).Request.Context(), resource, "UpdateAsset", map[string]any{"Id": "private-id"})
	require.Error(t, err)
	for _, secret := range []string{"test-ak", "test-sk", "private-id", "private.example"} {
		require.NotContains(t, err.Error(), secret)
	}
	_, err = bytePlusAssetRequest(seedanceContext(1).Request.Context(), resource, "GetAsset", map[string]any{"Id": "private-id"})
	require.Error(t, err)
	require.Equal(t, 2, calls)
	_, err = bytePlusAssetRequest(seedanceContext(1).Request.Context(), resource, "CreateAssetGroup", nil)
	require.Error(t, err)
	require.Equal(t, 503, err.(*SeedanceAPIError).Status)
	require.NotContains(t, err.Error(), "private subscription message")
	require.Equal(t, 3, calls)
	require.NoError(t, os.Remove(filepath.Join(os.Getenv("BYTEPLUS_ASSET_CREDENTIALS_DIR"), "channel-283.json")))
	_, err = loadBytePlusAssetCredentials(283)
	require.Error(t, err)
	c := seedanceContext(1)
	common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, "283")
	_, err = selectSeedanceLibrary(c, "seedance-2.5")
	require.Error(t, err)
	require.Equal(t, 3, calls)
	file := filepath.Join(os.Getenv("BYTEPLUS_ASSET_CREDENTIALS_DIR"), "channel-283.json")
	require.NoError(t, os.WriteFile(file, []byte(`{"access_key_id":"test-ak","secret_access_key":"test-sk","auto_route_enabled":false}`), 0600))
	_, err = selectSeedanceLibrary(seedanceContext(1), "seedance-2.5")
	require.Error(t, err)
	pinned, err := selectSeedanceLibrary(c, "seedance-2.5")
	require.NoError(t, err)
	require.Equal(t, 283, pinned.ChannelID)
	require.NoError(t, os.Chmod(file, 0644))
	_, err = loadBytePlusAssetCredentials(283)
	require.Error(t, err)
	var count int64
	require.NoError(t, db.Model(&model.SeedanceResource{}).Count(&count).Error)
	require.Zero(t, count)
}
