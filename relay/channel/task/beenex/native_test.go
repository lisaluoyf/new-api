package beenex

import (
	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNativeEditAndExtensionKeepMinusOneAndDoNotUseSeconds(t *testing.T) {
	for _, kind := range []string{"edit", "extend", "auto"} {
		duration := "-1"
		if kind == "extend" {
			duration = "4"
		}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/video/generations", strings.NewReader(`{"model":"seedance-2.5","prompt":"`+kind+` the reference video","duration":`+duration+`,"omni_reference_task_type":"`+kind+`","ratio":"adaptive","resolution":"480p","generate_audio":false,"watermark":false,"video_urls":["https://media.example/reference.mp4"]}`))
		c.Request.Header.Set("Content-Type", "application/json")
		info := &relaycommon.RelayInfo{OriginModelName: "seedance-2.5", TaskRelayInfo: &relaycommon.TaskRelayInfo{}, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 274, ChannelBaseUrl: "https://beenexapi.phone580.com", UpstreamModelName: "doubao-seedance-2-5-260628-hy"}}
		a := &TaskAdaptor{}
		a.Init(info)
		require.Nil(t, a.ValidateRequestAndSetAction(c, info))
		reader, err := a.BuildRequestBody(c, info)
		require.NoError(t, err)
		var body map[string]any
		require.NoError(t, common.DecodeJson(reader, &body))
		uri, err := a.BuildRequestURL(info)
		require.NoError(t, err)
		require.Equal(t, "https://beenexapi.phone580.com/api/v3/contents/generations/tasks", uri)
		for _, key := range []string{"seconds", "metadata", "prompt", "omni_reference_task_type", "output_format", "video_urls"} {
			require.NotContains(t, body, key)
		}
		for _, key := range []string{"generate_audio", "watermark"} {
			require.Equal(t, false, body[key])
		}
		require.NotContains(t, body, "seed")
		require.Equal(t, "adaptive", body["ratio"])
		if kind == "extend" {
			require.EqualValues(t, 4, body["duration"])
		} else {
			require.EqualValues(t, -1, body["duration"])
		}
		content := body["content"].([]any)
		require.Len(t, content, 2)
		require.Equal(t, "text", content[0].(map[string]any)["type"])
		require.Equal(t, "reference_video", content[1].(map[string]any)["role"])
	}
}

func TestNativeSeedIsRejectedBeforeReservation(t *testing.T) {
	_, err := generationPayload("seedance-2.5", map[string]any{"prompt": "Edit colors", "duration": float64(-1), "omni_reference_task_type": "edit", "aspect_ratio": "adaptive", "seed": float64(0), "video_urls": []any{"https://media.example/video.mp4"}})
	require.ErrorContains(t, err, "do not support seed")
}

func TestSeedance20NativeEditAndExtension(t *testing.T) {
	for _, kind := range []string{"edit", "extend", "auto"} {
		duration := "4"
		if kind == "auto" {
			duration = "-1"
		}
		if kind == "extend" {
			duration = "4"
		}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/video/generations", strings.NewReader(`{"model":"seedance-2.0","prompt":"`+kind+` the reference video","duration":`+duration+`,"omni_reference_task_type":"`+kind+`","ratio":"4:3","resolution":"480p","generate_audio":false,"watermark":false,"video_urls":["https://media.example/reference.mp4"]}`))
		c.Request.Header.Set("Content-Type", "application/json")
		info := &relaycommon.RelayInfo{OriginModelName: "seedance-2.0", TaskRelayInfo: &relaycommon.TaskRelayInfo{}, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 274, ChannelBaseUrl: "https://beenexapi.phone580.com", UpstreamModelName: "doubao-seedance-2-0-260128-hy"}}
		a := &TaskAdaptor{}
		a.Init(info)
		require.Nil(t, a.ValidateRequestAndSetAction(c, info))
		reader, err := a.BuildRequestBody(c, info)
		require.NoError(t, err)
		var body map[string]any
		require.NoError(t, common.DecodeJson(reader, &body))
		uri, err := a.BuildRequestURL(info)
		require.NoError(t, err)
		require.Equal(t, "https://beenexapi.phone580.com/api/v3/contents/generations/tasks", uri)
		for _, key := range []string{"seconds", "metadata", "prompt", "omni_reference_task_type", "output_format", "video_urls"} {
			require.NotContains(t, body, key)
		}
		for _, key := range []string{"generate_audio", "watermark"} {
			require.Equal(t, false, body[key])
		}
		require.NotContains(t, body, "seed")
		require.Equal(t, "4:3", body["ratio"])
		if kind == "auto" {
			require.EqualValues(t, -1, body["duration"])
		} else {
			require.EqualValues(t, 4, body["duration"])
		}
		content := body["content"].([]any)
		require.Len(t, content, 2)
		require.Equal(t, "text", content[0].(map[string]any)["type"])
		require.Equal(t, "reference_video", content[1].(map[string]any)["role"])
	}
}
