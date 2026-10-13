package beenex

import (
	"fmt"
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

func TestSeedance25QueryUsesSoraAndRejectsHTML(t *testing.T) {
	service.InitHttpClient()
	calls := 0
	status := 200
	contentType := "application/json"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "GET", r.Method)
		require.Equal(t, "/v1/videos/task_existing", r.URL.Path)
		require.Equal(t, "Bearer own-key", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		if status == 200 {
			fmt.Fprint(w, `{"id":"task_existing","status":"completed","progress":100,"completed_at":1791873949,"metadata":{"url":"https://cdn.example/video.mp4"}}`)
		} else {
			fmt.Fprint(w, "<html>alb</html>")
		}
	}))
	defer server.Close()
	a := &TaskAdaptor{}
	resp, err := a.FetchTask(server.URL, "own-key", map[string]any{"task_id": "task_existing", "origin_model_name": "seedance-2.5"}, "")
	require.NoError(t, err)
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.NoError(t, err)
	result, err := a.ParseTaskResult(raw)
	require.NoError(t, err)
	require.Equal(t, "SUCCESS", result.Status)
	require.Equal(t, "100%", result.Progress)
	require.EqualValues(t, 1791873949, result.CompletedAt)
	require.Equal(t, "https://cdn.example/video.mp4", result.Url)
	status = 502
	contentType = "text/html"
	_, err = a.FetchTask(server.URL, "own-key", map[string]any{"task_id": "task_existing", "origin_model_name": "seedance-2.5"}, "")
	require.ErrorContains(t, err, "HTTP 502")
	require.NotContains(t, err.Error(), "own-key")
	require.Equal(t, 2, calls)
}

func TestBeeNexUnsupportedEditAndMOVBeforeBilling(t *testing.T) {
	for _, name := range []string{"seedance-2.0", "seedance-2.5"} {
		for _, extra := range []string{`"duration":-1,"aspect_ratio":"adaptive","video_urls":["https://example.com/source.mp4"]`, `"duration":4,"aspect_ratio":"16:9","output_format":"mov"`, `"duration":4,"return_last_frame":true`, `"duration":4,"draft":true`} {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/videos", strings.NewReader(`{"model":"`+name+`","prompt":"edit source",`+extra+`}`))
			c.Request.Header.Set("Content-Type", "application/json")
			info := &relaycommon.RelayInfo{OriginModelName: name, TaskRelayInfo: &relaycommon.TaskRelayInfo{}, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 274, UpstreamModelName: "doubao-seedance-2-5-260628-hy"}}
			a := &TaskAdaptor{}
			a.Init(info)
			err := a.ValidateRequestAndSetAction(c, info)
			require.NotNil(t, err)
			require.Contains(t, err.Code, "unsupported")
			require.Nil(t, info.Billing)
			require.Empty(t, info.PublicTaskID)
		}
	}

}

func TestBothModelsConfiguredReadOnlyQueryAndNoRedirect(t *testing.T) {
	service.InitHttpClient()
	location := ""
	wantPath := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, wantPath, r.URL.Path)
		if location != "" {
			w.Header().Set("Location", location)
			w.WriteHeader(302)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"existing","status":"processing","progress":37}`)
	}))
	defer server.Close()
	for _, name := range []string{"seedance-2.0", "seedance-2.5"} {
		for _, protocol := range []string{"sora", "video_generations"} {
			wantPath = "/v1/videos/existing"
			if protocol == "video_generations" {
				wantPath = "/v1/video/generations/existing"
			}
			a := &TaskAdaptor{}
			resp, err := a.FetchTask(server.URL, "own-key", map[string]any{"task_id": "existing", "origin_model_name": name, "query_protocol": protocol}, "")
			require.NoError(t, err)
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			result, err := a.ParseTaskResult(raw)
			require.NoError(t, err)
			require.Equal(t, "37%", result.Progress)
			require.Equal(t, "IN_PROGRESS", result.Status)
		}
	}
	redirected := false
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected = true }))
	defer trap.Close()
	location = trap.URL
	wantPath = "/v1/videos/existing"
	_, err := (&TaskAdaptor{}).FetchTask(server.URL, "own-key", map[string]any{"task_id": "existing", "query_protocol": "sora"}, "")
	require.ErrorContains(t, err, "HTTP 302")
	require.False(t, redirected)
}
