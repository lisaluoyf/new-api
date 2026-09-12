package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
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
	require.NotContains(t, joined, "`")
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
	require.NotContains(t, joined, "`")
}

func TestFalseSuccessFinalResultNotificationFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set(common.RequestIdKey, "req-456")
	ctx.Set("channel_id", 216)
	ctx.Set("channel_name", "lingsu-gpt-luna")
	ctx.Set("use_channel", []string{"170", "216"})

	service.RecordUpstreamFalseSuccessAttempt(ctx, &types.UpstreamFalseSuccessDiagnostic{
		Trigger:   "response_error",
		ErrorCode: "server_error",
	})
	info := &relaycommon.RelayInfo{OriginModelName: "gpt-5.6-luna"}
	err := types.NewErrorWithStatusCode(context.DeadlineExceeded, types.ErrorCodeBadResponse, http.StatusBadGateway)

	title, lines, ok := buildUpstreamFalseSuccessResultNotification(ctx, info, err)
	require.True(t, ok)
	require.Contains(t, title, "HTTP 200 假成功 fallback 最终结果（失败）")
	joined := strings.Join(lines, "\n")
	require.Contains(t, joined, "fallback 是否成功：失败")
	require.Contains(t, joined, "最终状态：请求失败")
	require.Contains(t, joined, "fallback 渠道链路：170 -> 216")
	require.Contains(t, joined, "最终渠道：#216/lingsu-gpt-luna")
	require.Contains(t, joined, "上游错误代码：server_error")
	require.Contains(t, joined, "最终失败原因")
	require.NotContains(t, joined, "`")

	title, lines, ok = buildUpstreamFalseSuccessResultNotification(ctx, info, nil)
	require.True(t, ok)
	require.Contains(t, title, "HTTP 200 假成功 fallback 最终结果（成功）")
	joined = strings.Join(lines, "\n")
	require.Contains(t, joined, "fallback 是否成功：成功")
	require.Contains(t, joined, "最终状态：请求成功")
	require.NotContains(t, joined, "最终失败原因")
}
