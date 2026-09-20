package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestContentSafetyStopsAllRetryPlans(t *testing.T) {
	settings := model_setting.GetModelFallbackSettings()
	old := *settings
	*settings = model_setting.ModelFallbackSettings{Policies: []model_setting.OfficialFallbackPolicy{{Enabled: true, ModelID: "gpt-image-2.5-flare", OfficialChannelID: 102, FallbackAfter: 0}}}
	t.Cleanup(func() { *settings = old })
	for _, test := range []struct {
		name, code, kind, message string
		status                    int
	}{
		{"incident", "upstream_text_reply", "invalid_request_error", "非常抱歉，生成的图片可能违反了关于暴力内容的防护限制。如果你认为此判断有误，请重试或修改提示语。", 400},
		{"code", "content_policy_violation", "invalid_request_error", "Request rejected", 400},
		{"type", "unknown", "content_filter", "Request rejected", 503},
		{"risk", "unknown", "", "请求触发风控，已拒绝", 429},
		{"english", "upstream_text_reply", "", "Your request was rejected by the safety system.", 502},
		{"openai safety", "unknown", "", "Your request was rejected as a result of our safety system.", 400},
		{"azure", "ResponsibleAIPolicyViolation", "", "Rejected", 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/images/edits/async", nil)
			c.Set("original_model", "gpt-image-2.5-flare")
			err := types.WithOpenAIError(types.OpenAIError{Code: test.code, Type: test.kind, Message: test.message}, test.status)
			for _, decision := range []retryDecision{evaluateRetry(c, err, 0, 2), evaluateFreeModelRetry(c, err, 0, 2)} {
				require.False(t, decision.ShouldRetry)
				require.Equal(t, "content_safety_rejection", decision.Reason)
			}
			require.Equal(t, test.status, err.StatusCode)
			require.Equal(t, test.message, err.Error())
			code := test.code
			if test.name == "type" {
				code = test.kind
			}
			decision := evaluateTaskRetry(c, 73, &dto.TaskError{Code: code, Message: test.message, StatusCode: test.status}, 0, 2)
			require.False(t, decision.ShouldRetry)
			require.Equal(t, "content_safety_rejection", decision.Reason)
		})
	}
}

func TestContentSafetyDoesNotBlockOperationalFallback(t *testing.T) {
	for _, test := range []struct {
		code, message string
		status        int
	}{
		{"model_service_unavailable", "Model service unavailable. Try again later or choose another model.", 503},
		{"rate_limit_exceeded", "Rate limit exceeded", 429},
		{"upstream_text_reply", "The image service is unavailable", 400},
		{"invalid_request_error", "Unsupported image size", 400},
		{"server_error", "Content safety service unavailable", 503},
		{"access_denied", "Invalid API key", 403},
	} {
		t.Run(test.message, func(t *testing.T) {
			require.False(t, isContentSafetyRejection(test.code, "", test.message))
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/images/edits/async", nil)
			err := types.WithOpenAIError(types.OpenAIError{Code: test.code, Message: test.message}, test.status)
			require.True(t, evaluateRetry(c, err, 0, 2).ShouldRetry)
		})
	}
}
