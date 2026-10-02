package beenex

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPublicSeedanceMappingAndTransport(t *testing.T) {
	for _, name := range []string{"seedance-2.0", "seedance-2.5"} {
		for _, path := range []string{"/v1/video/generations", "/v1/videos/generations"} {
			t.Run(name+path, func(t *testing.T) {
				raw := `{"model":"` + name + `","prompt":"paper boat","duration":4,"resolution":"480p","aspect_ratio":"adaptive","generate_audio":false,"watermark":false,"seed":0,"image_with_roles":[{"url":"https://example.com/first.png","role":"first_frame"}],"video_urls":["https://example.com/ref.mp4"],"audio_urls":["https://example.com/ref.mp3"],"output_format":"mp4"}`
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest("POST", path, strings.NewReader(raw))
				c.Request.Header.Set("Content-Type", "application/json")
				info := &relaycommon.RelayInfo{OriginModelName: name, TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_public"}, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 274, ChannelBaseUrl: "https://beenexapi.phone580.com", ApiKey: "provider-key", UpstreamModelName: "doubao-" + name}}
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
				require.Equal(t, "4", out["seconds"])
				require.Equal(t, "paper boat", out["prompt"])
				metadata := out["metadata"].(map[string]any)
				require.Equal(t, "adaptive", metadata["ratio"])
				require.Equal(t, float64(4), metadata["duration"])
				require.Equal(t, false, metadata["generate_audio"])
				require.Equal(t, false, metadata["watermark"])
				require.Equal(t, float64(0), metadata["seed"])
				require.NotContains(t, out, "duration")
				require.NotContains(t, out, "aspect_ratio")
				require.NotContains(t, out, "video_urls")
				content := metadata["content"].([]any)
				require.Len(t, content, 3)

				require.Equal(t, "reference_video", content[0].(map[string]any)["role"])
				require.Equal(t, "reference_audio", content[1].(map[string]any)["role"])
				require.Equal(t, "first_frame", content[2].(map[string]any)["role"])
				req := httptest.NewRequest("POST", "http://example.com", nil)
				require.NoError(t, a.BuildRequestHeader(c, req, info))
				require.Equal(t, "apimaster-task_public", req.Header.Get("Idempotency-Key"))
				require.Equal(t, "Bearer provider-key", req.Header.Get("Authorization"))
				endpoint, err := a.BuildRequestURL(info)
				require.NoError(t, err)
				require.Equal(t, "https://beenexapi.phone580.com/v1/video/generations", endpoint)
			})
		}
	}
}

func TestNestedTaskResponses(t *testing.T) {
	a := &TaskAdaptor{baseURL: "https://beenexapi.phone580.com"}
	result, err := a.ParseTaskResult([]byte(`{"code":"success","data":{"task_id":"upstream-id","status":"SUCCESS","quota":6400000,"user_id":131,"data":{"duration":4,"content":{"video_url":"https://cdn.example.com/video.mp4"},"usage":{"completion_tokens":999}}}}`))
	require.NoError(t, err)
	require.Equal(t, "https://cdn.example.com/video.mp4", result.Url)
	require.Equal(t, 4, result.BillableSeconds)
	require.Zero(t, result.TotalTokens)
	for status, want := range map[string]string{"QUEUED": model.TaskStatusQueued, "IN_PROGRESS": model.TaskStatusInProgress, "FAILURE": model.TaskStatusFailure, "CANCELLED": model.TaskStatusFailure} {
		result, err = a.ParseTaskResult([]byte(`{"code":"success","data":{"status":"` + status + `","progress":"60%","fail_reason":"failure"}}`))
		require.NoError(t, err)
		require.Equal(t, want, result.Status)
	}
	_, err = a.ParseTaskResult([]byte(`{"code":"fail_to_fetch_task","message":"auth failed"}`))
	require.Error(t, err)
	task := &model.Task{TaskID: "public-id", ChannelId: 274, Status: model.TaskStatusSuccess}
	task.Properties.OriginModelName = "seedance-2.0"
	raw, err := a.ConvertToOpenAIVideo(task)
	require.NoError(t, err)
	require.Contains(t, string(raw), "public-id")
	require.NotContains(t, string(raw), "user_id")
	require.NotContains(t, string(raw), "quota")
	require.NotContains(t, string(raw), "beenexapi")
}
func TestPublicAliasesAndConflict(t *testing.T) {
	for _, extra := range []string{`"seconds":"4","size":"480p","ratio":"9:16"`, `"duration":4,"resolution":"480p","aspect_ratio":"9:16"`} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/video/generations", strings.NewReader(`{"model":"seedance-2.0","prompt":"boat",`+extra+`}`))
		c.Request.Header.Set("Content-Type", "application/json")
		info := &relaycommon.RelayInfo{OriginModelName: "seedance-2.0", TaskRelayInfo: &relaycommon.TaskRelayInfo{}, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 274, UpstreamModelName: "doubao-seedance-2-0-260128-hy"}}
		a := &TaskAdaptor{}
		a.Init(info)
		require.Nil(t, a.ValidateRequestAndSetAction(c, info))
		body, err := a.BuildRequestBody(c, info)
		require.NoError(t, err)
		raw, err := io.ReadAll(body)
		require.NoError(t, err)
		var payload map[string]any
		require.NoError(t, common.Unmarshal(raw, &payload))
		require.Equal(t, "4", payload["seconds"])
		require.Equal(t, "9:16", payload["metadata"].(map[string]any)["ratio"])
	}
	_, err := contentPayload(map[string]any{"duration": float64(4), "content": []any{}, "video_urls": []any{"https://example.com/ref.mp4"}})
	require.Error(t, err)
}
