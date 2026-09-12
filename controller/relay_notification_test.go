package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestShouldSuppressFinalFailureNotificationForClientDisconnectReasons(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name   string
		reason string
	}{
		{name: "client canceled", reason: "client_canceled"},
		{name: "client disconnected", reason: "client_disconnected"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			ctx.Request = req
			ctx.Set("retry_decision", map[string]interface{}{"reason": tc.reason})

			err := types.NewErrorWithStatusCode(context.Canceled, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
			require.True(t, shouldSuppressFinalFailureNotification(ctx, "claude-sonnet-5", err))
		})
	}
}

func TestShouldSuppressFinalFailureNotificationForCanceledRequestContext(t *testing.T) {
	gin.SetMode(gin.TestMode)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	reqCtx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(reqCtx)
	ctx.Request = req

	err := types.NewErrorWithStatusCode(context.Canceled, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	require.True(t, shouldSuppressFinalFailureNotification(ctx, "claude-sonnet-5", err))
}

func TestShouldSuppressFinalFailureNotificationKeepsRealFailures(t *testing.T) {
	gin.SetMode(gin.TestMode)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set("retry_decision", map[string]interface{}{"reason": "retry_times_exhausted"})

	err := types.NewErrorWithStatusCode(context.DeadlineExceeded, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	require.False(t, shouldSuppressFinalFailureNotification(ctx, "claude-sonnet-5", err))
}

func TestUpstreamFalseSuccessLogAndNotificationFields(t *testing.T) {
	diagnostic := types.UpstreamFalseSuccessDiagnostic{
		Trigger:            "response_error",
		UpstreamHTTPStatus: http.StatusOK,
		Stream:             true,
		EventType:          "response.failed",
		ResponseStatus:     "failed",
		ErrorType:          "server_error",
		ErrorCode:          "server_is_overloaded",
		ErrorMessage:       "Selected model is at capacity",
		StreamEndReason:    "handler_stop",
		RawResponse:        "{\"type\":\"response.failed\"}",
		Action:             "discard_channel_and_fallback",
	}
	err := types.NewOpenAIError(context.DeadlineExceeded, types.ErrorCodeBadResponse, http.StatusBadGateway)
	err.SetUpstreamFalseSuccess(diagnostic)

	other := map[string]interface{}{}
	appendUpstreamFalseSuccessLogInfo(other, err)
	require.Same(t, err.UpstreamFalseSuccess, other["upstream_false_success"])

	lines := upstreamFalseSuccessNotificationLines(err.UpstreamFalseSuccess)
	joined := strings.Join(lines, "\n")
	require.Contains(t, joined, "response.failed")
	require.Contains(t, joined, "server_is_overloaded")
	require.Contains(t, joined, "Selected model is at capacity")
	require.Contains(t, joined, "discard_channel_and_fallback")
	require.Contains(t, joined, "触发原因")
	require.Contains(t, joined, "错误代码")
	require.Contains(t, joined, "上游原始响应")
	require.NotContains(t, joined, "user_id")
	require.NotContains(t, joined, "email")
}

func TestFalseSuccessRequestContextNotificationFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(common.RequestIdKey, "req-123")
	ctx.Set("channel_id", 219)
	ctx.Set("channel_name", "lingsu-gpt-pro")
	ctx.Set("original_model", "gpt-5.6-terra")

	lines := falseSuccessRequestContextLines(ctx)
	joined := strings.Join(lines, "\n")
	require.Contains(t, joined, "请求 ID")
	require.Contains(t, joined, "#219/lingsu-gpt-pro")
	require.Contains(t, joined, "模型")
	require.Contains(t, joined, "gpt-5.6-terra")
}
