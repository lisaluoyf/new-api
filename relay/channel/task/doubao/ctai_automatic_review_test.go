package doubao

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
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

type ctaiReviewTransport func(*http.Request) (*http.Response, error)

func (f ctaiReviewTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestCtaiAdaptorReviewsAndResubmitsOnSameChannel(t *testing.T) {
	oldDB, oldMemory := model.DB, common.MemoryCacheEnabled
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { model.DB = oldDB; common.MemoryCacheEnabled = oldMemory; s, _ := db.DB(); _ = s.Close() })
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.SeedanceResource{}))
	base := "https://ai.ctaigw.cn"
	require.NoError(t, db.Create(&model.Channel{Id: 298, Type: 54, Key: "inference-key", BaseURL: &base, Status: 1, Models: "seedance-2.5", Group: "default"}).Error)
	generations, controls := 0, 0
	transport := ctaiReviewTransport(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "ai.ctaigw.cn", r.URL.Host)
		require.Equal(t, "Bearer inference-key", r.Header.Get("Authorization"))
		status, body := 200, `{}`
		switch r.URL.Path {
		case "/v1/api/assets/upload":
			controls++
			body = `{"code":0,"data":{"Id":"369481078005587968"}}`
		case "/v1/api/assets/369481078005587968":
			controls++
			body = `{"code":0,"data":{"Id":"369481078005587968","Status":"Active"}}`
		case "/v1/contents/generations/tasks":
			generations++
			var payload map[string]any
			require.NoError(t, common.DecodeJson(r.Body, &payload))
			if generations == 1 {
				status = 400
				body = `{"error":{"code":"InputImageSensitiveContentDetected.PrivacyInformation","message":"input may contain real person"}}`
			} else {
				require.Equal(t, 2, generations)
				img := payload["content"].([]any)[0].(map[string]any)["image_url"].(map[string]any)
				require.Equal(t, "asset://369481078005587968", img["url"])
				body = `{"id":"created-video"}`
			}
		default:
			t.Fatalf("unexpected endpoint %s", r.URL.Path)
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
	info := &relaycommon.RelayInfo{OriginModelName: "seedance-2.5", TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_xingchi_adaptor"}, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 298, ChannelBaseUrl: base, ApiKey: "inference-key"}}
	adaptor := &TaskAdaptor{}
	adaptor.Init(info)
	resp, err := adaptor.DoRequest(c, info, bytes.NewReader(raw))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	require.Equal(t, 2, generations)
	require.Equal(t, 2, controls)
	require.Equal(t, "task_xingchi_adaptor", info.PublicTaskID)
	require.Equal(t, 298, info.ChannelId)
	var count int64
	require.NoError(t, db.Model(&model.SeedanceResource{}).Where("channel_id = ?", 298).Count(&count).Error)
	require.EqualValues(t, 2, count)
}

func TestCtaiFetchDoesNotAdvertiseAnAbsentJSONBody(t *testing.T) {
	transport := ctaiReviewTransport(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "https://ai.ctaigw.cn/v1/contents/generations/tasks/native-task", r.URL.String())
		require.Equal(t, "Bearer inference-key", r.Header.Get("Authorization"))
		require.Empty(t, r.Header.Get("Content-Type"))
		require.Nil(t, r.Body)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"native-task","status":"succeeded","content":{"video_url":"https://example.com/result.mp4"}}`))}, nil
	})
	if service.GetHttpClient() == nil {
		service.InitHttpClient()
	}
	client := service.GetHttpClient()
	old := client.Transport
	client.Transport = transport
	t.Cleanup(func() { client.Transport = old })
	a := &TaskAdaptor{}
	resp, err := a.FetchTask("https://ai.ctaigw.cn", "inference-key", map[string]any{"task_id": "native-task"}, "")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
}
