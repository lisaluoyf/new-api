package hailuo

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestH3OfficialContentThroughValidationAndForwarding(t *testing.T) {
	const content = `[{"type":"text","text":"Keep the character in Image 1; use motion from Video 1 and sound from Audio 1."},{"type":"image_url","role":"reference_image","image_url":{"url":"https://example.com/reference.png"}},{"type":"video_url","role":"reference_video","video_url":{"url":"https://example.com/reference.mp4"}},{"type":"audio_url","role":"reference_audio","audio_url":{"url":"https://example.com/reference.mp3"}}]`
	for _, prefix := range []string{"", `"prompt":"legacy fallback must not replace content",`} {
		t.Run(prefix, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", strings.NewReader(`{`+prefix+`"model":"MiniMax-H3","content":`+content+`,"resolution":"2K","duration":8,"ratio":"9:16","aigc_watermark":false,"callback_url":"https://example.com/callback","metadata":{"resolution":"768P","ratio":"16:9","content":[{"type":"text","text":"wrong"}]}}`))
			c.Request.Header.Set("Content-Type", "application/json")
			info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}, ChannelMeta: &relaycommon.ChannelMeta{}}
			a := &H3TaskAdaptor{}
			require.Nil(t, a.ValidateRequestAndSetAction(c, info))
			request, err := relaycommon.GetTaskRequest(c)
			require.NoError(t, err)
			require.Contains(t, request.Prompt, "Image 1")
			require.Equal(t, "2K", request.Size)
			captured := make(chan []byte, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/v2/video_generation", r.URL.Path)
				var body map[string]interface{}
				require.NoError(t, common.DecodeJson(r.Body, &body))
				data, err := common.Marshal(body)
				require.NoError(t, err)
				captured <- data
				w.Write([]byte(`{"task_id":"mock-task"}`))
			}))
			defer server.Close()
			info.ChannelBaseUrl = server.URL
			info.ApiKey = "test-only"
			a.Init(info)
			endpoint, err := a.BuildRequestURL(info)
			require.NoError(t, err)
			reader, err := a.BuildRequestBody(c, info)
			require.NoError(t, err)
			req, err := http.NewRequest(http.MethodPost, endpoint, reader)
			require.NoError(t, err)
			require.NoError(t, a.BuildRequestHeader(c, req, info))
			response, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			response.Body.Close()
			body := <-captured
			require.JSONEq(t, content, gjson.GetBytes(body, "content").Raw)
			require.False(t, gjson.GetBytes(body, "prompt").Exists())
			require.Equal(t, "2K", gjson.GetBytes(body, "resolution").String())
			require.Equal(t, "9:16", gjson.GetBytes(body, "ratio").String())
			require.Equal(t, int64(8), gjson.GetBytes(body, "duration").Int())
			require.True(t, gjson.GetBytes(body, "aigc_watermark").Exists())
			require.False(t, gjson.GetBytes(body, "aigc_watermark").Bool())
			require.Equal(t, "https://example.com/callback", gjson.GetBytes(body, "callback_url").String())
		})
	}
}

func TestH3ContentValidationAndLegacyCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, fields, role string
		bad                bool
	}{
		{"legacy-images", `"prompt":"animate","images":["https://example.com/a.png"]`, "first_frame", false},
		{"metadata-no-prompt", `"metadata":{"content":[{"type":"text","text":"Image 1"},{"type":"image_url","role":"reference_image","image_url":{"url":"https://example.com/a.png"}}]}`, "reference_image", false},
		{"empty", `"content":[]`, "", true},
		{"empty-with-prompt", `"prompt":"do not silently fall back","content":[]`, "", true},
		{"null", `"content":null`, "", true},
		{"missing-text", `"content":[{"type":"image_url","role":"reference_image","image_url":{"url":"https://example.com/a.png"}}]`, "", true},
		{"bad-shape", `"content":"invalid"`, "", true},
		{"whitespace", `"content":[{"type":"text","text":" "}]`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", strings.NewReader(`{"model":"MiniMax-H3",`+tc.fields+`}`))
			c.Request.Header.Set("Content-Type", "application/json")
			a := &H3TaskAdaptor{}
			info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}, ChannelMeta: &relaycommon.ChannelMeta{}}
			taskErr := a.ValidateRequestAndSetAction(c, info)
			if tc.bad {
				require.NotNil(t, taskErr)
				require.Equal(t, 400, taskErr.StatusCode)
				return
			}
			require.Nil(t, taskErr)
			reader, err := a.BuildRequestBody(c, info)
			require.NoError(t, err)
			var body h3CreateRequest
			require.NoError(t, common.DecodeJson(reader, &body))
			require.Len(t, body.Content, 2)
			require.Equal(t, tc.role, body.Content[1].Role)
		})
	}
}
