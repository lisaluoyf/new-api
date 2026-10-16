package doubao

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type tokenHubReviewTransport func(*http.Request) (*http.Response, error)

func (f tokenHubReviewTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestTokenHubAdaptorReviewsAndResubmitsOnSameChannel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TOKENHUB_ASSET_CREDENTIALS_DIR", dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "channel-297.json"), []byte(`{"access_key_id":"pak_test","secret_access_key":"psk_test"}`), 0600))
	oldDB, oldMemory := model.DB, common.MemoryCacheEnabled
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { model.DB = oldDB; common.MemoryCacheEnabled = oldMemory; s, _ := db.DB(); _ = s.Close() })
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.SeedanceResource{}))
	base := "https://ai-tokenhub.ai"
	require.NoError(t, db.Create(&model.Channel{Id: 297, Type: 54, Key: "inference-key", BaseURL: &base, Status: 1, Models: "seedance-2.5", Group: "default"}).Error)
	generations, controls := 0, 0
	transport := tokenHubReviewTransport(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "ai-tokenhub.ai", r.URL.Host)
		if r.URL.Path == "/ark" {
			require.Equal(t, "Bearer pak_test:psk_test", r.Header.Get("Authorization"))
		} else {
			require.Equal(t, "/api/v2/contents/generations/tasks", r.URL.Path)
			require.Equal(t, "Bearer inference-key", r.Header.Get("Authorization"))
		}
		var payload map[string]any
		require.NoError(t, common.DecodeJson(r.Body, &payload))
		status, body := 200, `{}`
		if action := r.URL.Query().Get("Action"); action != "" {
			controls++
			switch action {
			case "CreateAssetGroup":
				body = `{"Result":{"Id":"review-group"}}`
			case "CreateAsset":
				body = `{"Result":{"Id":"review-image"}}`
			case "GetAsset":
				body = `{"Result":{"Status":"Active"}}`
			default:
				t.Fatalf("unexpected control action %s", action)
			}
		} else {
			generations++
			if generations == 1 {
				status = 400
				body = `{"error":"InputImageSensitiveContentDetected.PrivacyInformation","errorCode":10508,"message":"image may contain real person"}`
			} else {
				require.Equal(t, 2, generations)
				img := payload["content"].([]any)[0].(map[string]any)["image_url"].(map[string]any)
				require.Equal(t, "asset://review-image", img["url"])
				body = `{"id":"created-video"}`
			}
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	oldTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = oldTransport })
	if service.GetHttpClient() == nil {
		service.InitHttpClient()
	}
	client := service.GetHttpClient()
	oldClientTransport := client.Transport
	client.Transport = transport
	t.Cleanup(func() { client.Transport = oldClientTransport })
	raw := []byte(`{"model":"doubao-seedance-2-5-260628","content":[{"type":"image_url","image_url":{"url":"https://example.com/cartoon.png"},"role":"reference_image"}],"duration":4,"seed":0,"generate_audio":false}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos/generations", bytes.NewReader(raw))
	c.Set("id", 1)
	info := &relaycommon.RelayInfo{OriginModelName: "seedance-2.5", TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_xingchi_adaptor"}, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 297, ChannelBaseUrl: base, ApiKey: "inference-key"}}
	adaptor := &TaskAdaptor{}
	adaptor.Init(info)
	resp, err := adaptor.DoRequest(c, info, bytes.NewReader(raw))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	require.Equal(t, 2, generations)
	require.Equal(t, 3, controls)
	require.Equal(t, "task_xingchi_adaptor", info.PublicTaskID)
	require.Equal(t, 297, info.ChannelId)
	var count int64
	require.NoError(t, db.Model(&model.SeedanceResource{}).Where("channel_id = ?", 297).Count(&count).Error)
	require.EqualValues(t, 2, count)
}
