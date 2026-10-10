package videofee

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPublicSeedanceMappingAndTransport(t *testing.T) {
	for _, name := range []string{"seedance-2.0", "seedance-2.0-fast", "seedance-2.0-mini", "seedance-2.5"} {
		for _, path := range []string{"/v1/video/generations", "/v1/videos/generations"} {
			t.Run(name+path, func(t *testing.T) {
				raw := `{"model":"` + name + `","prompt":"paper boat","duration":4,"resolution":"480p","aspect_ratio":"adaptive","generate_audio":false,"watermark":false,"seed":0,"image_with_roles":[{"url":"https://example.com/first.png","role":"reference_image"}],"video_urls":["https://example.com/ref.mp4"],"audio_urls":["https://example.com/ref.mp3"],"output_format":"mp4"}`
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest("POST", path, strings.NewReader(raw))
				c.Request.Header.Set("Content-Type", "application/json")
				info := &relaycommon.RelayInfo{OriginModelName: name, TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_public"}, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 273, ChannelBaseUrl: "https://seedance2026.vip", ApiKey: "provider-key", UpstreamModelName: "doubao-" + name}}
				a := &TaskAdaptor{}
				a.Init(info)
				require.Nil(t, a.ValidateRequestAndSetAction(c, info))
				reader, err := a.BuildRequestBody(c, info)
				require.NoError(t, err)
				data, err := io.ReadAll(reader)
				require.NoError(t, err)
				var out map[string]any
				require.NoError(t, common.Unmarshal(data, &out))
				require.Equal(t, "doubao-"+name, out["model"])
				require.Equal(t, "adaptive", out["ratio"])
				require.Equal(t, float64(4), out["duration"])
				require.Equal(t, false, out["generate_audio"])
				require.Equal(t, false, out["watermark"])
				require.Equal(t, float64(0), out["seed"])
				require.NotContains(t, out, "prompt")
				require.NotContains(t, out, "aspect_ratio")
				require.NotContains(t, out, "video_urls")
				content := out["content"].([]any)
				require.Len(t, content, 4)
				require.Equal(t, map[string]any{"type": "text", "text": "paper boat"}, content[0])
				require.Equal(t, "reference_video", content[1].(map[string]any)["role"])
				require.Equal(t, "reference_audio", content[2].(map[string]any)["role"])
				require.Equal(t, "reference_image", content[3].(map[string]any)["role"])
				req := httptest.NewRequest("POST", "http://example.com", nil)
				require.NoError(t, a.BuildRequestHeader(c, req, info))
				require.Equal(t, "apimaster-task_public", req.Header.Get("Idempotency-Key"))
				require.Equal(t, "Bearer provider-key", req.Header.Get("Authorization"))
				endpoint, err := a.BuildRequestURL(info)
				require.NoError(t, err)
				require.Equal(t, "https://seedance2026.vip/v1/video/generations", endpoint)
			})
		}
	}
}

func TestDefaultRolesAndConflicts(t *testing.T) {
	out, err := contentPayload(map[string]any{"prompt": "boat", "aspect_ratio": "16:9", "image_urls": []any{"https://example.com/ref.png"}})
	require.NoError(t, err)
	require.Equal(t, "reference_image", out["content"].([]any)[1].(map[string]any)["role"])
	_, err = contentPayload(map[string]any{"content": []any{}, "video_urls": []any{"https://example.com/ref.mp4"}})
	require.Error(t, err)
	for _, fields := range []string{`"ratio":"16:9","aspect_ratio":"9:16"`, `"seconds":"4","duration":5`, `"size":"1080p","resolution":"480p"`} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/videos/generations", strings.NewReader(`{"model":"seedance-2.0","prompt":"scene",`+fields+`}`))
		c.Request.Header.Set("Content-Type", "application/json")
		info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 273}}
		a := &TaskAdaptor{}
		a.Init(info)
		require.NotNil(t, a.ValidateRequestAndSetAction(c, info))
		require.Nil(t, info.Billing)
	}
}

func TestDraftUpgradeUsesNativeReferenceWithoutInheritedBillingFields(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/videos/generations", nil)
	c.Request.Header.Set("Content-Type", "application/json")
	// Normalization derives duration/audio for the gateway's own billing.
	// These values must never become overrides in the provider upgrade request.
	c.Set("apimart_normalized_request", map[string]any{
		"model": "seedance-2.5", "draft_task_id": "task_public_draft",
		"duration": 4, "generate_audio": false, "aspect_ratio": "16:9", "seed": 0,
		"omni_reference_task_type": "auto", "nsfw_check": false,
		"resolution": "1080p", "output_format": "mp4", "return_last_frame": true, "watermark": false,
	})
	c.Set("task_request", relaycommon.TaskSubmitReq{Model: "seedance-2.5", Duration: 4})
	c.Set("seedance_draft_task", &model.Task{
		TaskID: "task_public_draft", PrivateData: model.TaskPrivateData{UpstreamTaskID: "provider-draft-id"},
	})
	info := &relaycommon.RelayInfo{
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
		ChannelMeta:   &relaycommon.ChannelMeta{ChannelId: 273, UpstreamModelName: "doubao-seedance-2-5"},
	}
	a := &TaskAdaptor{}
	a.Init(info)
	reader, err := a.BuildRequestBody(c, info)
	require.NoError(t, err)
	raw, err := io.ReadAll(reader)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, common.Unmarshal(raw, &out))
	require.Equal(t, map[string]any{
		"model": "doubao-seedance-2-5", "resolution": "1080p", "output_format": "mp4",
		"return_last_frame": true, "watermark": false,
		"content": []any{map[string]any{"type": "draft_task", "draft_task": map[string]any{"id": "provider-draft-id"}}},
	}, out)
	req, err := relaycommon.GetTaskRequest(c)
	require.NoError(t, err)
	require.Equal(t, 4, req.Duration)
}

func TestStatusesAndBillingIsolation(t *testing.T) {
	a := &TaskAdaptor{}
	for raw, want := range map[string]string{
		`{"code":"success","data":{"status":"QUEUED","progress":"0%"}}`:                                                                                                     model.TaskStatusQueued,
		`{"code":"success","data":{"status":"IN_PROGRESS","progress":"60%"}}`:                                                                                               model.TaskStatusInProgress,
		`{"code":"success","data":{"status":"SUCCESS","result_url":"https://seedance2026.vip/v1/videos/uuid/content","billing":{"currency":"CNY","reserved_amount":"99"}}}`: model.TaskStatusSuccess,
		`{"code":"success","data":{"status":"FAILURE","fail_reason":"provider failed"}}`:                                                                                    model.TaskStatusFailure,
		`{"code":"success","data":{"status":"CANCELLED"}}`:                                                                                                                  model.TaskStatusFailure,
	} {
		result, err := a.ParseTaskResult([]byte(raw))
		require.NoError(t, err)
		require.Equal(t, want, result.Status)
		require.Zero(t, result.TotalTokens)
		require.Zero(t, result.BillableSeconds)
	}
	for _, raw := range []string{`{"data":{"status":"UNKNOWN"}}`, `{"data":{"status":"SUCCESS"}}`, `invalid`} {
		_, err := a.ParseTaskResult([]byte(raw))
		require.Error(t, err)
	}
	task := &model.Task{TaskID: "task_public", Status: model.TaskStatusSuccess, ChannelId: constant.VideoFeeSeedanceChannelID, Properties: model.Properties{OriginModelName: "seedance-2.0"}, Data: []byte(`{"data":{"billing":{"currency":"CNY","reserved_amount":"99"}}}`)}
	data, err := a.ConvertToOpenAIVideo(task)
	require.NoError(t, err)
	require.NotContains(t, string(data), "CNY")
	require.NotContains(t, string(data), "reserved_amount")
	require.Contains(t, string(data), "task_public")
}

func TestTaskTypeIsOnlySentForReferences(t *testing.T) {
	for _, media := range []map[string]any{
		{"prompt": "boat", "omni_reference_task_type": "auto"},
		{"prompt": "boat", "omni_reference_task_type": "auto", "image_with_roles": []any{map[string]any{"url": "https://example.com/first.png", "role": "first_frame"}}},
	} {
		out, err := contentPayload(media)
		require.NoError(t, err)
		require.NotContains(t, out, "omni_reference_task_type")
	}
	out, err := contentPayload(map[string]any{"prompt": "boat", "omni_reference_task_type": "reference", "video_urls": []any{"https://example.com/ref.mp4"}})
	require.NoError(t, err)
	require.Equal(t, "reference", out["omni_reference_task_type"])
}

func TestPollingUsesDocumentedEndpoint(t *testing.T) {
	service.InitHttpClient()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/video/generations/upstream-id", r.URL.Path)
		require.Equal(t, "Bearer provider-key", r.Header.Get("Authorization"))
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `{"data":{"status":"QUEUED"}}`)
	}))
	defer server.Close()
	resp, err := (&TaskAdaptor{}).FetchTask(server.URL, "provider-key", map[string]any{"task_id": "upstream-id"}, "")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
}

func TestRelativeResultURLUsesProviderBase(t *testing.T) {
	a := &TaskAdaptor{baseURL: "https://seedance2026.vip"}
	r, err := a.ParseTaskResult([]byte(`{"data":{"status":"SUCCESS","result_url":"/v1/videos/provider-id/content"}}`))
	require.NoError(t, err)
	require.Equal(t, "https://seedance2026.vip/v1/videos/provider-id/content", r.Url)
	_, err = (&TaskAdaptor{}).ParseTaskResult([]byte(`{"data":{"status":"SUCCESS","result_url":"/v1/videos/id/content"}}`))
	require.Error(t, err)
}
