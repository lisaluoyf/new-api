package gmiwan

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

func TestQueueSubmissionAndPolling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service.InitHttpClient()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer secret-test", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			require.Equal(t, queuePath, r.URL.Path)
			var fields map[string]any
			require.NoError(t, common.DecodeJson(r.Body, &fields))
			payload := fields["payload"].(map[string]any)
			require.Equal(t, "720P", payload["resolution"])
			require.Equal(t, float64(2), payload["duration"])
			require.Equal(t, false, payload["watermark"])
			require.Equal(t, false, payload["prompt_extend"])
			require.Equal(t, float64(0), payload["seed"])
			io.WriteString(w, `{"request_id":"provider-id","status":"queued"}`)
		} else {
			require.Equal(t, queuePath+"/provider-id", r.URL.Path)
			io.WriteString(w, `{"request_id":"provider-id","status":"success","updated_at":123,"outcome":{"media_urls":[{"id":"0","url":"https://storage.googleapis.com/test/video.mp4"}]}}`)
		}
	}))
	defer upstream.Close()
	a := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	info.ChannelBaseUrl = upstream.URL
	info.ApiKey = "secret-test"
	info.OriginModelName = "wan3.0-video-prime"
	info.UpstreamModelName = "wan3.0-video-prime"
	info.PublicTaskID = "public-id"
	a.Init(info)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/v1/videos", strings.NewReader(`{"model":"wan3.0-video-prime","prompt":"paper boat","seconds":"2","watermark":false,"prompt_extend":false,"seed":0}`))
	c.Request.Header.Set("Content-Type", "application/json")
	require.Nil(t, a.ValidateRequestAndSetAction(c, info))
	require.Equal(t, constant.TaskActionTextGenerate, info.Action)
	require.Equal(t, float64(2), a.EstimateBilling(c, info)["seconds"])
	body, err := a.BuildRequestBody(c, info)
	require.NoError(t, err)
	reqURL, err := a.BuildRequestURL(info)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, reqURL, body)
	require.NoError(t, err)
	require.NoError(t, a.BuildRequestHeader(c, req, info))
	response, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	id, _, taskErr := a.DoResponse(c, response, info)
	require.Nil(t, taskErr)
	require.Equal(t, "provider-id", id)
	require.Contains(t, recorder.Body.String(), "public-id")
	require.NotContains(t, recorder.Body.String(), "provider-id")
	response, err = a.FetchTask(upstream.URL, "secret-test", map[string]any{"task_id": id}, "")
	require.NoError(t, err)
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	result, err := a.ParseTaskResult(raw)
	require.NoError(t, err)
	require.Equal(t, model.TaskStatusSuccess, result.Status)
	require.Equal(t, "https://storage.googleapis.com/test/video.mp4", result.Url)
	public, err := a.ConvertToOpenAIVideo(&model.Task{TaskID: "public-id", Status: model.TaskStatusSuccess})
	require.NoError(t, err)
	require.Contains(t, string(public), "public-id")
	require.NotContains(t, string(public), "storage.googleapis.com")
}
func TestInvalidRequestsBeforeReservation(t *testing.T) {
	for _, raw := range []string{
		`{"prompt":"boat","duration":1}`, `{"prompt":"boat","duration":2.5}`, `{"prompt":"boat","duration":2,"seconds":"3"}`, `{"prompt":"boat","resolution":"4k"}`, `{"prompt":"boat","resolution":"480P","size":"1920x1080"}`, `{"prompt":"boat","watermark":"false"}`, `{"prompt":"boat","seed":-1}`, `{"prompt":"boat","reference_video":"https://example.com/v.mp4"}`, `{"prompt":"boat","video":"https://example.com/v.mp4"}`,
	} {
		t.Run(raw, func(t *testing.T) {
			var fields map[string]any
			require.NoError(t, common.Unmarshal([]byte(raw), &fields))
			_, err := normalize(fields)
			require.Error(t, err)
		})
	}
}
func TestStatusAndMissingMedia(t *testing.T) {
	a := &TaskAdaptor{}
	for status, want := range map[string]model.TaskStatus{"queued": model.TaskStatusQueued, "dispatched": model.TaskStatusQueued, "processing": model.TaskStatusInProgress, "failed": model.TaskStatusFailure, "cancelled": model.TaskStatusFailure} {
		raw, err := common.Marshal(map[string]any{"request_id": "id", "status": status, "error": map[string]any{"message": "provider failure detail"}})
		require.NoError(t, err)
		result, err := a.ParseTaskResult(raw)
		require.NoError(t, err)
		require.Equal(t, string(want), result.Status)
		if want == model.TaskStatusFailure {
			require.Equal(t, "provider failure detail", result.Reason)
		}
	}
	for _, raw := range []string{`{"request_id":"id","status":"unknown"}`, `{"request_id":"id","status":"success"}`, `{"request_id":"id","status":"success","outcome":{"video_url":"javascript:alert(1)"}}`} {
		_, err := a.ParseTaskResult([]byte(raw))
		require.Error(t, err)
	}
	require.Equal(t, "https://console.gmicloud.ai", queueBase("https://api.gmi-serving.com"))
	require.False(t, IsChannel("https://api.gmi-serving.com.evil.test"))
}
func TestMismatchedTaskAndRedirectRejected(t *testing.T) {
	service.InitHttpClient()
	for _, redirect := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if redirect {
				http.Redirect(w, r, "https://example.com/leak", 302)
			} else {
				io.WriteString(w, `{"request_id":"different-task","status":"processing"}`)
			}
		}))
		a := &TaskAdaptor{}
		_, err := a.FetchTask(server.URL, "secret", map[string]any{"task_id": "expected-task"}, "")
		require.Error(t, err)
		server.Close()
	}
}
