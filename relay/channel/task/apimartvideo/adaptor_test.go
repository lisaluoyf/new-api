package apimartvideo

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestParseTaskResultCompleted(t *testing.T) {
	body := []byte(`{
	  "code": 200,
	  "data": {
	    "status": "completed",
	    "progress": 100,
	    "result": {
	      "videos": [{"url": ["https://upload.apib.ai/f/demo.mp4"]}]
	    }
	  }
	}`)
	a := &TaskAdaptor{}
	info, err := a.ParseTaskResult(body)
	require.NoError(t, err)
	require.Equal(t, model.TaskStatusSuccess, info.Status)
	require.Equal(t, "https://upload.apib.ai/f/demo.mp4", info.Url)
}

func TestNormalizeModel(t *testing.T) {
	require.Equal(t, "sora-2", normalizeModel("sora"))
	require.True(t, IsVideoModel("sora"))
	require.Equal(t, ModelDoubaoSeedance20, normalizeModel(ModelDoubaoSeedance20))
	require.True(t, IsVideoModel(ModelDoubaoSeedance20))
	require.True(t, IsVideoModel(ModelGrokImagineVideo15))
	require.True(t, IsVideoModel(ModelGrokVideo10s))
	require.True(t, IsVideoModel(ModelGrokVideo15s))
	require.True(t, IsVideoModel(ModelGrokVideo6s))
}

func TestIsChannel(t *testing.T) {
	require.True(t, IsChannel("https://api.apimart.ai"))
	require.True(t, IsChannel("https://api.apib.ai"))
	require.False(t, IsChannel("https://api.openai.com"))
}

func TestNormalizeVideoDuration(t *testing.T) {
	require.Equal(t, 6, normalizeVideoDuration(ModelGrokImagineVideo15, 4))
	require.Equal(t, 6, normalizeVideoDuration(ModelGrokImagineVideo15, 6))
	require.Equal(t, 8, normalizeVideoDuration(ModelGrokImagineVideo15, 8))
	require.Equal(t, 10, normalizeVideoDuration(ModelGrokVideo10s, 6))
	require.Equal(t, 15, normalizeVideoDuration(ModelGrokVideo15s, 10))
	require.Equal(t, 4, normalizeVideoDuration("sora-2", 0))
	require.Equal(t, 5, normalizeVideoDuration(ModelKlingV3Omni, 0))
	require.Equal(t, 5, normalizeVideoDuration(ModelKlingV3Omni, 2))
	require.Equal(t, 3, normalizeVideoDuration(ModelKlingV3Omni, 3))
	require.Equal(t, 6, normalizeVideoDuration(ModelKlingV3Omni, 6))
	require.Equal(t, 10, normalizeVideoDuration(ModelKlingV3Omni, 10))
	require.Equal(t, 15, normalizeVideoDuration(ModelKlingV3Omni, 15))
	require.Equal(t, 5, normalizeVideoDuration(ModelKlingV3Omni, 16))
}

func TestKlingOmniEstimateBillingUsesModeAndMedia(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name     string
		mode     string
		audio    bool
		hasVideo bool
		variant  string
	}{
		{name: "base", mode: "std", variant: "base"},
		{name: "sound", mode: "std", audio: true, variant: "sound"},
		{name: "video", mode: "std", audio: true, hasVideo: true, variant: "video"},
		{name: "pro", mode: "pro", variant: "pro"},
		{name: "pro sound", mode: "pro", audio: true, variant: "pro-sound"},
		{name: "pro video", mode: "pro", hasVideo: true, variant: "pro-video"},
		{name: "4k", mode: "4k", variant: "4k"},
		{name: "4k sound", mode: "4k", audio: true, variant: "4k-sound"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set("task_request", relaycommon.TaskSubmitReq{
				Model:    ModelKlingV3Omni,
				Duration: 10,
				Metadata: map[string]interface{}{
					"mode":      tc.mode,
					"audio":     tc.audio,
					"has_video": tc.hasVideo,
				},
			})
			got := (&TaskAdaptor{}).EstimateBilling(c, &relaycommon.RelayInfo{})
			require.Equal(t, 10.0, got["seconds"])
			require.InDelta(
				t,
				ratio_setting.GetVideoModelPriceRatio(ModelKlingV3Omni, tc.variant),
				got["variant"],
				1e-9,
			)
		})
	}
}

func TestKlingOmniBuildRequestPreservesMultimodalFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	raw := []byte(`{
		"model":"kling-v3-omni",
		"prompt":"scene",
		"mode":"pro",
		"duration":6,
		"audio":true,
		"negative_prompt":"blur",
		"multi_shot":true,
		"video_list":[{"video_url":"https://example.com/ref.mp4","refer_type":"feature"}]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/videos/generations", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	info := &relaycommon.RelayInfo{
		ChannelMeta:   &relaycommon.ChannelMeta{UpstreamModelName: ModelKlingV3Omni},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
	adaptor := &TaskAdaptor{}

	require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
	ratios := adaptor.EstimateBilling(c, info)
	require.Equal(t, 6.0, ratios["seconds"])
	require.InDelta(
		t,
		ratio_setting.GetVideoModelPriceRatio(ModelKlingV3Omni, "pro-video"),
		ratios["variant"],
		1e-9,
	)

	reader, err := adaptor.BuildRequestBody(c, info)
	require.NoError(t, err)
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	text := string(body)
	for _, expected := range []string{
		`"model":"kling-v3-omni"`,
		`"mode":"pro"`,
		`"duration":6`,
		`"audio":true`,
		`"negative_prompt":"blur"`,
		`"multi_shot":true`,
		`"video_list"`,
	} {
		require.True(t, strings.Contains(text, expected), "body missing %s: %s", expected, text)
	}
}

func TestKlingOmniRejectsInvalidVideoURLs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name      string
		videoList string
		wantError bool
	}{
		{name: "empty", videoList: `[{"video_url":""}]`, wantError: true},
		{name: "whitespace", videoList: `[{"video_url":"   "}]`, wantError: true},
		{name: "relative URL", videoList: `[{"video_url":"/v1/videos/task/content"}]`, wantError: true},
		{name: "unsupported scheme", videoList: `[{"video_url":"ftp://example.com/ref.mp4"}]`, wantError: true},
		{name: "more than one", videoList: `[{"video_url":"https://example.com/a.mp4"},{"video_url":"https://example.com/b.mp4"}]`, wantError: true},
		{name: "public HTTPS URL", videoList: `[{"video_url":"https://example.com/ref.mp4"}]`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(`{"model":"kling-v3-omni","prompt":"scene","duration":3,"video_list":` + tc.videoList + `}`)
			req := httptest.NewRequest(http.MethodPost, "/v1/videos/generations", bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = req
			info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}

			taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(c, info)
			if tc.wantError {
				require.NotNil(t, taskErr)
				return
			}
			require.Nil(t, taskErr)
		})
	}
}
