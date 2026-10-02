package service

import (
	"fmt"
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
)

func TestVideoFeePublicMediaLibraryContract(t *testing.T) {
	db := seedanceTestDB(t)
	key := "test-provider-key"
	imports := 0
	active := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer "+key, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/real-person-assets/import-url":
			var request map[string]any
			require.NoError(t, common.DecodeJson(r.Body, &request))
			require.Contains(t, request["url"], "https://example.com/")
			imports++
			fmt.Fprintf(w, `{"row":{"assetKey":"provider%d","status":"certifying","uploadStatus":"uploaded"}}`, imports)
		case strings.HasPrefix(r.URL.Path, "/api/assets/"):
			status := "certifying"
			if active {
				status = "active"
				if strings.HasSuffix(r.URL.Path, "provider2") {
					status = "blocked"
				}
			}
			fmt.Fprintf(w, `{"row":{"status":%q,"uploadStatus":"uploaded","assetCertification":{"status":"active"}}}`, status)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	base := server.URL
	require.NoError(t, db.Create(&model.Channel{Id: constant.VideoFeeSeedanceChannelID, Key: key, BaseURL: &base, Status: 1, Models: "seedance-2.5", Group: "default"}).Error)
	require.NoError(t, db.Create(&model.Ability{ChannelId: 273, Model: "seedance-2.5", Group: "default", Enabled: true}).Error)
	model.InitChannelCache()
	c := seedanceContext(1)
	common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, "273")
	group, err := CreateSeedanceGroup(c, "seedance-2.5", SeedanceGroupInput{Name: "My assets"})
	require.NoError(t, err)
	require.Equal(t, group.ID, group.UpstreamID)
	require.Zero(t, imports)
	task, err := SubmitSeedanceAssets(c, SeedanceAssetSubmission{Model: "seedance-2.5", GroupID: group.ID, AssetType: "Image", Assets: []SeedanceAssetInput{{URL: "https://example.com/one.png"}, {URL: "https://example.com/two.png"}}})
	require.NoError(t, err)
	require.Equal(t, 2, imports)
	require.NoError(t, PollSeedanceAssetTask(c, task))
	require.Equal(t, "processing", task.Status)
	active = true
	require.NoError(t, PollSeedanceAssetTask(c, task))
	require.Equal(t, "failed", task.Status)
	var ids []string
	require.NoError(t, common.UnmarshalJsonStr(task.RequestData, &ids))
	a, err := model.GetSeedanceResource(1, "asset", ids[0])
	require.NoError(t, err)
	require.Equal(t, "Active", a.Status)
	b, err := model.GetSeedanceResource(1, "asset", ids[1])
	require.NoError(t, err)
	require.Equal(t, "Failed", b.Status)
	_, err = ResolveSeedanceAssetReferences(seedanceContext(2), map[string]any{"image_urls": []any{"asset://" + a.ID}}, 273, key)
	require.Error(t, err)
	forwarded, err := ResolveSeedanceAssetReferences(c, map[string]any{"image_urls": []any{"asset://" + a.ID}}, 273, key)
	require.NoError(t, err)
	require.Equal(t, []any{"asset://provider1"}, forwarded["image_urls"])
	public, err := common.Marshal(SeedanceTaskDTO(task))
	require.NoError(t, err)
	require.NotContains(t, string(public), key)
	require.NotContains(t, string(public), "provider1")
	require.NotContains(t, string(public), "provider2")
}

func TestVideoFeeLocalUploadAndMultipart(t *testing.T) {
	oldDir, oldBase := imageCacheDir, imageCachePublicBase
	imageCacheDir, imageCachePublicBase = t.TempDir(), "https://apimaster.ai/imgs/"
	t.Cleanup(func() { imageCacheDir, imageCachePublicBase = oldDir, oldBase })
	filename := "media_upload_task_test.png"
	data := []byte("\x89PNG\r\n\x1a\nfixture")
	require.NoError(t, os.WriteFile(filepath.Join(imageCacheDir, filename), data, 0600))
	for _, source := range []string{"https://other.example/file.png", oldBase + "../secret", oldBase + "media_upload_task_x%2fsecret", oldBase + "media_upload_task_x?other"} {
		upload, err := videoFeeLocalUpload(source)
		require.NoError(t, err)
		require.Nil(t, upload)
	}
	upload, err := videoFeeLocalUpload(imageCachePublicBase + filename)
	require.NoError(t, err)
	require.Equal(t, data, upload.data)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Contains(t, []string{"/api/real-person-assets/upload", "/api/assets/upload"}, r.URL.Path)
		require.NoError(t, r.ParseMultipartForm(1<<20))
		defer r.MultipartForm.RemoveAll()
		file, header, err := r.FormFile("file")
		require.NoError(t, err)
		defer file.Close()
		require.Equal(t, "image/png", header.Header.Get("Content-Type"))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"row":{"assetKey":"provider-test"}}`)
	}))
	defer server.Close()
	base := server.URL
	result, err := seedanceProviderRawRequest(t.Context(), &model.SeedanceResource{}, &model.Channel{BaseURL: &base}, "key", http.MethodPost, "/api/real-person-assets/upload", *upload)
	require.NoError(t, err)
	require.Contains(t, result, "row")
	image := map[string]any{"url": imageCachePublicBase + filename}
	content := []any{map[string]any{"type": "image_url", "role": "first_frame", "image_url": image}}
	require.NoError(t, ResolveVideoFeeUploadedImages(t.Context(), base, "key", content))
	require.Equal(t, "asset://provider-test", image["url"])
	require.Equal(t, "first_frame", content[0].(map[string]any)["role"])
}
