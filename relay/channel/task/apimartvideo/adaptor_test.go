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

func TestConvertToOpenAIVideoHidesProviderFailure(t *testing.T) {
	task := &model.Task{
		TaskID:     "task_public",
		Status:     model.TaskStatusFailure,
		FailReason: "The video width should not be less than 700px",
		Data:       []byte(`{"error":{"message":"provider rejected width"}}`),
	}
	body, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	require.NoError(t, err)
	require.Contains(t, string(body), "Video generation failed")
	require.NotContains(t, string(body), "700px")
	require.NotContains(t, string(body), "provider rejected")
}

func TestConvertToOpenAIVideoProxiesProviderResult(t *testing.T) {
	task := &model.Task{
		TaskID: "task_public",
		Status: model.TaskStatusSuccess,
		PrivateData: model.TaskPrivateData{
			ResultURL: "https://upload.apib.ai/provider-task.mp4",
		},
	}
	body, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	require.NoError(t, err)
	require.Contains(t, string(body), "/v1/videos/task_public/content")
	require.NotContains(t, string(body), "apib.ai")
	require.NotContains(t, string(body), "provider-task")
}

func TestDoResponseHidesProviderSubmissionError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(`{
			"code":422,
			"error":{"message":"provider-specific validation details"}
		}`)),
	}

	_, _, taskErr := (&TaskAdaptor{}).DoResponse(c, resp, &relaycommon.RelayInfo{})
	require.NotNil(t, taskErr)
	require.Equal(t, "video service rejected the request", taskErr.Message)
	require.NotContains(t, taskErr.Message, "provider-specific")
}

func TestNormalizeModel(t *testing.T) {
	require.Equal(t, "sora-2", normalizeModel("sora"))
	require.True(t, IsVideoModel("sora"))
	require.Equal(t, ModelDoubaoSeedance20, normalizeModel(ModelDoubaoSeedance20))
	require.True(t, IsVideoModel(ModelDoubaoSeedance20))
	require.True(t, IsVideoModel(ModelSeedance25))
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
	require.Equal(t, -1, normalizeVideoDuration(ModelSeedance25, -1))
	require.Equal(t, 4, normalizeVideoDuration(ModelSeedance25, 0))
	require.Equal(t, 30, normalizeVideoDuration(ModelSeedance25, 31))
}

func TestSeedance25AutoDurationReservesThirtySecondsAndPreservesRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	raw := []byte(`{"model":"seedance-2.5","prompt":"scene","duration":-1,"resolution":"1080p","generate_audio":true,"audio_urls":["https://example.com/ref.mp3"],"output_format":"mov"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/videos/generations", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: ModelSeedance25}, TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	a := &TaskAdaptor{}
	require.Nil(t, a.ValidateRequestAndSetAction(c, info))
	ratios := a.EstimateBilling(c, info)
	require.Equal(t, 30.0, ratios["seconds"])
	require.InDelta(t, 0.38488/0.216, ratios["size"], 1e-9)

	reader, err := a.BuildRequestBody(c, info)
	require.NoError(t, err)
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	for _, expected := range []string{`"model":"seedance-2.5"`, `"duration":-1`, `"generate_audio":true`, `"audio_urls":["https://example.com/ref.mp3"]`, `"output_format":"mov"`} {
		require.Contains(t, string(body), expected)
	}
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

func TestSeedanceEstimateBillingUsesConfiguredResolutionPrices(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		resolution string
		hasVideo   bool
		price      float64
	}{
		{resolution: "480P", price: 0.066},
		{resolution: "720P", price: 0.142},
		{resolution: "1080P", price: 0.3544},
		{resolution: "4K", price: 0.722},
		{resolution: "480P", hasVideo: true, price: 0.04},
		{resolution: "720P", hasVideo: true, price: 0.08584},
		{resolution: "1080P", hasVideo: true, price: 0.21568},
		{resolution: "4K", hasVideo: true, price: 0.44432},
	}
	for _, tc := range tests {
		name := tc.resolution
		if tc.hasVideo {
			name += "-input"
		}
		t.Run(name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set("task_request", relaycommon.TaskSubmitReq{
				Model:    ModelDoubaoSeedance20,
				Duration: 4,
				Metadata: map[string]interface{}{
					"resolution": tc.resolution,
					"has_video":  tc.hasVideo,
				},
			})
			got := (&TaskAdaptor{}).EstimateBilling(c, &relaycommon.RelayInfo{})
			require.Equal(t, 4.0, got["seconds"])
			require.InDelta(t, tc.price/0.142, got["size"], 1e-9)
		})
	}
}

func TestSeedanceBuildRequestPreservesReferenceMediaFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	raw := []byte(`{
		"model":"doubao-seedance-2.0",
		"prompt":"scene",
		"duration":8,
		"resolution":"1080p",
		"size":"9:16",
		"generate_audio":true,
		"video_urls":["https://example.com/ref.mp4"],
		"audio_urls":["https://example.com/ref.mp3"]
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/videos/generations", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	info := &relaycommon.RelayInfo{
		ChannelMeta:   &relaycommon.ChannelMeta{UpstreamModelName: ModelDoubaoSeedance20},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
	a := &TaskAdaptor{}
	require.Nil(t, a.ValidateRequestAndSetAction(c, info))
	ratios := a.EstimateBilling(c, info)
	require.InDelta(t, 0.21568/0.142, ratios["size"], 1e-9)

	reader, err := a.BuildRequestBody(c, info)
	require.NoError(t, err)
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	text := string(body)
	for _, expected := range []string{
		`"video_urls":["https://example.com/ref.mp4"]`,
		`"audio_urls":["https://example.com/ref.mp3"]`,
		`"generate_audio":true`,
		`"size":"9:16"`,
	} {
		require.Contains(t, text, expected)
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
