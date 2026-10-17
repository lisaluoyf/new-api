package apimartveo

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T, name, upstream, extra string) (*TaskAdaptor, *gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/videos", strings.NewReader(`{"model":"`+name+`","prompt":"a paper boat on a lake"`+extra+`}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("model_mapping", `{"`+name+`":"`+upstream+`"}`)
	info := &relaycommon.RelayInfo{OriginModelName: name, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.apimart.ai"}, TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	a := &TaskAdaptor{}
	a.Init(info)
	return a, c, info
}

func TestOfficialAndExtPayloadsAndBilling(t *testing.T) {
	for _, tc := range []struct {
		name, upstream, extra, res string
		seconds, ratio             float64
	}{
		{Standard, "veo3.1-quality-official", `,"duration":4,"generate_audio":false,"seed":0`, "720p", 4, .5},
		{Fast, "veo3.1-fast-official", `,"duration":8,"resolution":"1080p"`, "1080p", 8, 1.2},
		{Fast, "veo3.1-fast-official", `,"duration":8,"resolution":"4k"`, "4K", 8, 3},
		{Standard, "veo3.1-quality-ext", ``, "720p", 8, 1},
		{Fast, "veo3.1-fast-ext", `,"seconds":"8","size":"1080p"`, "1080p", 8, 1.2},
	} {
		t.Run(tc.upstream+tc.res, func(t *testing.T) {
			a, c, info := fixture(t, tc.name, tc.upstream, tc.extra)
			require.Nil(t, a.ValidateRequestAndSetAction(c, info))
			r, err := a.BuildRequestBody(c, info)
			require.NoError(t, err)
			raw, err := io.ReadAll(r)
			require.NoError(t, err)
			var body map[string]any
			require.NoError(t, common.Unmarshal(raw, &body))
			require.Equal(t, tc.upstream, body["model"])
			require.Equal(t, tc.res, body["resolution"])
			require.Equal(t, tc.seconds, body["duration"])
			ratios := a.EstimateBilling(c, info)
			require.Equal(t, tc.seconds, ratios["seconds"])
			require.InDelta(t, tc.ratio, ratios["size"], 1e-9)
			if tc.extra == `,"duration":4,"generate_audio":false,"seed":0` {
				require.Equal(t, false, body["generate_audio"])
				require.Equal(t, float64(0), body["seed"])
			}
			if strings.HasSuffix(tc.upstream, "-ext") {
				require.NotContains(t, body, "generate_audio")
			}
		})
	}
}

func TestVeoInvalidRequestsRejectedBeforeBilling(t *testing.T) {
	for _, extra := range []string{
		`,"duration":0`, `,"duration":8.5`, `,"duration":"bad"`, `,"duration":8,"seconds":"4"`,
		`,"sample_count":2`, `,"official_fallback":true`, `,"resolution":"8k"`,
		`,"image_urls":["file:///etc/passwd"]`, `,"image_urls":"https://example.com/a.png"`, `,"aspect_ratio":"1:1"`,
		`,"ratio":"16:9","aspect_ratio":"9:16"`, `,"generation_type":"reference","image_urls":["https://example.com/a.png"],"aspect_ratio":"9:16"`,
		`,"nsfw_check":true`, `,"input_reference":5`, `,"generation_type":false`, `,"negative_prompt":7`, `,"person_generation":"unknown"`, `,"resize_mode":"stretch"`, `,"ratio":"9:16","size":"1280x720"`,
	} {
		t.Run(extra, func(t *testing.T) {
			a, c, info := fixture(t, Fast, "veo3.1-fast-ext", extra)
			err := a.ValidateRequestAndSetAction(c, info)
			require.NotNil(t, err)
			require.Equal(t, 400, err.StatusCode)
			_, exists := c.Get("task_request")
			require.False(t, exists)
		})
	}
	a, c, info := fixture(t, Standard, "veo3.1-quality-official", `,"duration":4,"resolution":"1080p"`)
	require.NotNil(t, a.ValidateRequestAndSetAction(c, info))
	a, c, info = fixture(t, Standard, "veo3.1-quality-official", ``)
	info.Action = constant.TaskActionRemix
	require.NotNil(t, a.ValidateRequestAndSetAction(c, info))
}

func TestVeoRouteCapabilityMismatchCanFallbackWithoutCharge(t *testing.T) {
	for _, extra := range []string{`,"duration":6`, `,"generate_audio":false`, `,"negative_prompt":"blur"`, `,"seed":0`} {
		a, c, info := fixture(t, Fast, "veo3.1-fast-ext", extra)
		err := a.ValidateRequestAndSetAction(c, info)
		require.NotNil(t, err)
		require.Equal(t, 503, err.StatusCode)
		require.True(t, err.LocalError)
		require.Equal(t, "veo_route_unsupported", err.Code)
		_, exists := c.Get("veo_request")
		require.False(t, exists)
	}
}

func TestVeoFrameTranslation(t *testing.T) {
	for _, upstream := range []string{"veo3.1-fast-official", "veo3.1-fast-ext"} {
		a, c, info := fixture(t, Fast, upstream, `,"first_frame_image":"https://example.com/a.png","last_frame_image":"https://example.com/b.png"`)
		require.Nil(t, a.ValidateRequestAndSetAction(c, info))
		r, err := a.BuildRequestBody(c, info)
		require.NoError(t, err)
		var out map[string]any
		require.NoError(t, common.DecodeJson(r, &out))
		if strings.HasSuffix(upstream, "official") {
			require.Equal(t, "https://example.com/a.png", out["first_frame_image"])
			require.NotContains(t, out, "image_urls")
		} else {
			require.Len(t, out["image_urls"], 2)
			require.NotContains(t, out, "first_frame_image")
		}
	}
}

func TestVeoSubmitAndTerminalContract(t *testing.T) {
	a, c, info := fixture(t, Fast, "veo3.1-fast-ext", ``)
	info.PublicTaskID = "task_public"
	id, _, taskErr := a.DoResponse(c, &http.Response{Body: io.NopCloser(strings.NewReader(`{"code":200,"data":[{"task_id":"private_upstream","status":"submitted"}]}`))}, info)
	require.Nil(t, taskErr)
	require.Equal(t, "private_upstream", id)
	require.NotContains(t, c.Writer.Header().Get("Location"), "private_upstream")
	result, err := a.ParseTaskResult([]byte(`{"code":200,"data":{"status":"completed","result":{"videos":[{"url":["https://example.com/result.mp4"]}]}}}`))
	require.NoError(t, err)
	require.Equal(t, model.TaskStatusSuccess, result.Status)
	require.Equal(t, "https://example.com/result.mp4", result.Url)
	_, err = a.ParseTaskResult([]byte(`{"code":200,"data":{"status":"completed"}}`))
	require.Error(t, err)
	failed, err := a.ParseTaskResult([]byte(`{"code":200,"data":{"status":"failed","error":{"message":"generation failed"}}}`))
	require.NoError(t, err)
	require.Equal(t, model.TaskStatusFailure, failed.Status)
	require.True(t, IsChannel("https://api.apib.ai"))
	require.False(t, IsChannel("https://api.apib.ai.attacker.test"))
}

func TestVeoThreeReferencesCanFallbackFromOfficialToExt(t *testing.T) {
	extra := `,"image_urls":["https://example.com/1.png","https://example.com/2.png","https://example.com/3.png"]`
	a, c, info := fixture(t, Fast, "veo3.1-fast-official", extra)
	err := a.ValidateRequestAndSetAction(c, info)
	require.NotNil(t, err)
	require.Equal(t, 503, err.StatusCode)
	require.True(t, err.LocalError)
	a, c, info = fixture(t, Fast, "veo3.1-fast-ext", extra)
	require.Nil(t, a.ValidateRequestAndSetAction(c, info))
	body, _ := c.Get("veo_request")
	require.Equal(t, "reference", body.(map[string]any)["generation_type"])
	a, c, info = fixture(t, Standard, "veo3.1-quality-official", extra)
	err = a.ValidateRequestAndSetAction(c, info)
	require.NotNil(t, err)
	require.Equal(t, 400, err.StatusCode)
}

func TestVeoExplicitRejectionDoesNotCreateAmbiguousBillingHold(t *testing.T) {
	a, c, info := fixture(t, Fast, "veo3.1-fast", "")
	id, _, taskErr := a.DoResponse(c, &http.Response{Body: io.NopCloser(strings.NewReader(`{"success":false,"message":"unsupported model"}`))}, info)
	require.Empty(t, id)
	require.NotNil(t, taskErr)
	require.Equal(t, 400, taskErr.StatusCode)
	require.Equal(t, "upstream_rejected", taskErr.Code)
	_, _, taskErr = a.DoResponse(c, &http.Response{Body: io.NopCloser(strings.NewReader(`{"code":200,"data":[]}`))}, info)
	require.NotNil(t, taskErr)
	require.Equal(t, 502, taskErr.StatusCode, "unknown acceptance must stay ambiguous")
}

func TestVeoPublicProgressUsesOpenAIVideoNumericContract(t *testing.T) {
	for _, progress := range []string{"0%", "50%", "100%"} {
		task := &model.Task{TaskID: "task_public", Status: model.TaskStatusSuccess, Progress: progress, Properties: model.Properties{OriginModelName: Fast}, PrivateData: model.TaskPrivateData{UpstreamTaskID: "provider_private"}}
		raw, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
		require.NoError(t, err)
		var video dto.OpenAIVideo
		require.NoError(t, common.Unmarshal(raw, &video))
		require.Equal(t, dto.VideoStatusCompleted, video.Status)
		require.Contains(t, video.Metadata["url"], "/v1/videos/task_public/content")
		require.NotContains(t, string(raw), "provider_private")
	}
}
