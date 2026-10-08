package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

func TestClassifyChannelError_platformUserQuota(t *testing.T) {
	t.Parallel()
	err := types.NewErrorWithStatusCode(
		types.NewError(nil, types.ErrorCodeInsufficientUserQuota),
		types.ErrorCodeInsufficientUserQuota,
		403,
	)
	require.Equal(t, CategorySkip, ClassifyChannelError(err))
}

func TestClassifyChannelError_wrappedPlatformUserQuota(t *testing.T) {
	t.Parallel()
	err := types.NewErrorWithStatusCode(
		types.NewError(nil, types.ErrorCodeBadResponseStatusCode),
		types.ErrorCodeBadResponseStatusCode,
		403,
	)
	err.SetMessage("status_code=403, 用户额度不足, 剩余额度: ＄-0.009978")
	require.Equal(t, CategorySkip, ClassifyChannelError(err))
}

func TestClassifyChannelError_distributorNoAvailableUsesProbe(t *testing.T) {
	t.Parallel()
	err := types.NewErrorWithStatusCode(
		types.NewError(nil, types.ErrorCodeBadResponseStatusCode),
		types.ErrorCodeBadResponseStatusCode,
		503,
	)
	err.SetMessage("No available channel for model gpt-5.4 under group A-Codex-Sale (distributor)")
	require.Equal(t, CategoryDisableWindow, ClassifyChannelError(err))

	commonBackup := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = commonBackup })
	action, reason := EvaluateChannelHealth(types.ChannelError{ChannelId: 197, AutoBan: true}, err)
	require.Equal(t, HealthProbeBeforeDisable, action)
	require.Contains(t, reason, "distributor")
}

func TestClassifyChannelError_modelAccessForbidden(t *testing.T) {
	t.Parallel()
	err := types.NewErrorWithStatusCode(
		types.NewError(nil, types.ErrorCodeBadResponseStatusCode),
		types.ErrorCodeBadResponseStatusCode,
		403,
	)
	err.SetMessage("status_code=403, 该令牌无权访问模型 claude-opus-4-7")
	require.Equal(t, CategoryDisableImmediate, ClassifyChannelError(err))
}

func TestClassifyChannelError_moonshotMissingModelIsNotRecharge(t *testing.T) {
	t.Parallel()
	err := types.NewErrorWithStatusCode(
		types.NewError(nil, types.ErrorCodeBadResponseStatusCode),
		types.ErrorCodeBadResponseStatusCode,
		404,
	)
	err.SetMessage("status_code=404, Not found the model kimi-k2.5 or Permission denied")

	require.Equal(t, CategoryDisableImmediate, ClassifyChannelError(err))
	require.False(t, IsHighConfidenceRecharge(err))
}

func TestClassifyChannelError_upstreamModelNotFoundRequiresProbe(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  *types.NewAPIError
	}{
		{
			name: "structured model_not_found",
			err: types.WithOpenAIError(types.OpenAIError{
				Message: "unknown provider for model gpt-5.6-sol",
				Type:    "invalid_request_error",
				Code:    string(types.ErrorCodeModelNotFound),
			}, http.StatusBadRequest),
		},
		{
			name: "provider omits error code",
			err: func() *types.NewAPIError {
				err := types.NewErrorWithStatusCode(
					types.NewError(nil, types.ErrorCodeBadResponseStatusCode),
					types.ErrorCodeBadResponseStatusCode,
					http.StatusBadRequest,
				)
				err.SetMessage("status_code=400, unknown provider for model gpt-5.6-sol")
				return err
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, CategoryProbeBeforeDisable, ClassifyChannelError(tt.err))
		})
	}
}

func TestEvaluateChannelHealthModelNotFoundResponseRequiresProbe(t *testing.T) {
	previous := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() {
		common.AutomaticDisableChannelEnabled = previous
		ClearChannelHealth(219)
	})
	for _, body := range []string{
		`{"error":{"message":"Model \"gpt-6-astra\" is not supported by any configured account in this group","type":"model_not_found"}}`,
		`{"error":{"message":"missing model","code":"model_not_found"}}`,
	} {
		err := RelayErrorHandler(context.Background(), &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader(body)),
		}, false)
		require.Equal(t, types.ErrorCodeModelNotFound, err.GetErrorCode())
		require.Equal(t, CategoryProbeBeforeDisable, ClassifyChannelError(err))
		action, _ := EvaluateChannelHealth(types.ChannelError{ChannelId: 219, AutoBan: true}, err)
		require.Equal(t, HealthProbeBeforeDisable, action)
		action, _ = EvaluateChannelHealth(types.ChannelError{ChannelId: 219, AutoBan: false}, err)
		require.Equal(t, HealthSkip, action)
		common.AutomaticDisableChannelEnabled = false
		action, _ = EvaluateChannelHealth(types.ChannelError{ChannelId: 219, AutoBan: true}, err)
		require.Equal(t, HealthSkip, action)
		common.AutomaticDisableChannelEnabled = true
	}
}

func TestClassifyChannelError_genericBadRequestDoesNotDisable(t *testing.T) {
	t.Parallel()
	err := types.NewErrorWithStatusCode(
		types.NewError(nil, types.ErrorCodeBadResponseStatusCode),
		types.ErrorCodeBadResponseStatusCode,
		http.StatusBadRequest,
	)
	err.SetMessage("status_code=400, invalid request parameter")

	require.Equal(t, CategorySkip, ClassifyChannelError(err))
}

func TestEvaluateChannelHealthUnavailableUpstreamGroup(t *testing.T) {
	previous := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() {
		common.AutomaticDisableChannelEnabled = previous
		ClearChannelHealth(257)
	})
	for _, body := range []string{
		`{"code":"GROUP_DELETED","message":"API Key 所属分组已删除"}`,
		`{"code":"GROUP_DELETED","message":"Group unavailable"}`,
		`{"error":{"code":"GROUP_DELETED","message":"Group unavailable"}}`,
		`{"message":"API Key 所属分组已删除"}`,
		`{"error":{"message":"API Key 所属分组已停用","type":"bad_response_status_code","param":"","code":"bad_response_status_code"}}`,
		`{"message":"API Key 所属分组已禁用"}`,
		`{"code":"GROUP_DISABLED","message":"Group unavailable"}`,
		`{"error":{"code":"group_disabled","message":"Group unavailable"}}`,
	} {
		t.Run(body, func(t *testing.T) {
			err := RelayErrorHandler(context.Background(), &http.Response{
				StatusCode: http.StatusForbidden,
				Body:       io.NopCloser(strings.NewReader(body)),
			}, false)
			require.Equal(t, CategoryCredentialInvalid, ClassifyChannelError(err))
			require.True(t, ShouldDisableChannel(err))
			action, reason := EvaluateChannelHealth(types.ChannelError{ChannelId: 257, AutoBan: true}, err)
			require.Equal(t, HealthDisableCredential, action, "must disable the key/channel, not just one model")
			require.NotEmpty(t, reason)
			action, _ = EvaluateChannelHealth(types.ChannelError{ChannelId: 257, AutoBan: false}, err)
			require.Equal(t, HealthSkip, action)
			common.AutomaticDisableChannelEnabled = false
			action, _ = EvaluateChannelHealth(types.ChannelError{ChannelId: 257, AutoBan: true}, err)
			require.Equal(t, HealthSkip, action)
			require.False(t, ShouldDisableChannel(err))
			common.AutomaticDisableChannelEnabled = true
		})
	}
}

func TestClassifyChannelErrorUnavailableGroupBoundaries(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		status   int
		body     string
		category ChannelErrorCategory
	}{
		{"generic forbidden", 403, `{"message":"Forbidden"}`, CategorySkip},
		{"user quota takes precedence", 403, `{"code":"GROUP_DELETED","message":"用户额度不足"}`, CategorySkip},
		{"model access remains model scoped", 403, `{"message":"该令牌无权访问模型 claude-opus-4-7"}`, CategoryDisableImmediate},
		{"code mentioned in message is not a code", 403, `{"message":"unsupported parameter GROUP_DELETED"}`, CategorySkip},
		{"bad parameter is not credential failure", 400, `{"code":"GROUP_DELETED","message":"invalid parameter"}`, CategorySkip},
		{"transient failure stays in window", 503, `{"code":"GROUP_DELETED","message":"Service temporarily unavailable"}`, CategoryDisableWindow},
		{"disabled group on unauthorized", 401, `{"error":{"message":"API Key 所属分组已停用","code":"bad_response_status_code"}}`, CategoryCredentialInvalid},
		{"disabled group user quota takes precedence", 403, `{"code":"GROUP_DISABLED","message":"用户额度不足"}`, CategorySkip},
		{"disabled group code mentioned as parameter", 403, `{"message":"unsupported parameter GROUP_DISABLED"}`, CategorySkip},
		{"group policy is not a key diagnosis", 403, `{"message":"分组已停用该模型"}`, CategorySkip},
		{"disabled group on bad request", 400, `{"message":"API Key 所属分组已停用"}`, CategorySkip},
		{"disabled group on transient response", 503, `{"message":"API Key 所属分组已停用"}`, CategoryDisableWindow},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := RelayErrorHandler(context.Background(), &http.Response{
				StatusCode: tt.status,
				Body:       io.NopCloser(strings.NewReader(tt.body)),
			}, false)
			require.Equal(t, tt.category, ClassifyChannelError(err))
		})
	}
}

func TestClassifyChannelError_legacyDisableKeywordIsNotRecharge(t *testing.T) {
	t.Parallel()
	err := types.NewErrorWithStatusCode(
		types.NewError(nil, types.ErrorCodeBadResponseStatusCode),
		types.ErrorCodeBadResponseStatusCode,
		400,
	)
	err.SetMessage("Operation not allowed")

	require.Equal(t, CategoryDisableImmediate, ClassifyChannelError(err))
}

func TestClassifyChannelError_imageGenerationTimeout(t *testing.T) {
	t.Parallel()
	err := types.WithOpenAIError(types.OpenAIError{
		Message: "Image generation timed out after 600 seconds. Retry with lower resolution or quality.",
		Type:    "server_error",
		Code:    string(types.ErrorCodeImageGenerationTimeout),
	}, http.StatusRequestTimeout, types.ErrOptionWithNoRecordErrorLog(), types.ErrOptionWithSkipRetry())
	require.Equal(t, CategorySkip, ClassifyChannelError(err))
	require.False(t, ShouldDisableChannel(err))
}

func TestClassifyChannelError_windowFault502(t *testing.T) {
	t.Parallel()
	err := types.NewErrorWithStatusCode(
		types.NewError(nil, types.ErrorCodeBadResponseStatusCode),
		types.ErrorCodeBadResponseStatusCode,
		502,
	)
	err.SetMessage("status_code=502, bad response status code 502")
	require.Equal(t, CategoryDisableWindow, ClassifyChannelError(err))
}

func TestClassifyChannelError_providerConcurrency429(t *testing.T) {
	t.Parallel()
	for _, message := range []string{
		"status_code=429, Concurrency limit exceeded for account, please retry later",
		"status_code=429, Too many pending requests, please retry later",
		"upstream overload: rate_limit_error",
	} {
		err := types.NewErrorWithStatusCode(
			types.NewError(nil, types.ErrorCodeBadResponseStatusCode),
			types.ErrorCodeBadResponseStatusCode,
			429,
		)
		err.SetMessage(message)
		require.Equal(t, CategoryRateLimitWindow, ClassifyChannelError(err), message)
	}
}

func TestEvaluateChannelHealth_providerConcurrency429ProbeThreshold(t *testing.T) {
	resetChannelHealthForTest()
	ch := types.ChannelError{ChannelId: 199, ChannelName: "test", AutoBan: true}
	commonBackup := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() {
		common.AutomaticDisableChannelEnabled = commonBackup
		resetChannelHealthForTest()
	})

	make429 := func() *types.NewAPIError {
		err := types.NewErrorWithStatusCode(
			types.NewError(nil, types.ErrorCodeBadResponseStatusCode),
			types.ErrorCodeBadResponseStatusCode,
			429,
		)
		err.SetMessage("Concurrency limit exceeded for account, please retry later")
		return err
	}
	for i := 0; i < 7; i++ {
		action, _ := EvaluateChannelHealth(ch, make429())
		require.Equal(t, HealthSkip, action, "attempt %d should skip", i+1)
	}
	action, reason := EvaluateChannelHealth(ch, make429())
	require.Equal(t, HealthProbeBeforeDisable, action)
	require.Contains(t, reason, "429")
}

func TestEvaluateChannelHealth_consecutive502(t *testing.T) {
	resetChannelHealthForTest()
	ch := types.ChannelError{ChannelId: 99, ChannelName: "test", AutoBan: true}
	commonBackup := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() {
		common.AutomaticDisableChannelEnabled = commonBackup
		resetChannelHealthForTest()
	})

	make502 := func() *types.NewAPIError {
		e := types.NewErrorWithStatusCode(
			types.NewError(nil, types.ErrorCodeBadResponseStatusCode),
			types.ErrorCodeBadResponseStatusCode,
			502,
		)
		e.SetMessage("bad response status code 502")
		return e
	}

	for i := 0; i < 4; i++ {
		action, _ := EvaluateChannelHealth(ch, make502())
		require.Equal(t, HealthSkip, action, "attempt %d should skip", i+1)
	}
	action, reason := EvaluateChannelHealth(ch, make502())
	require.Equal(t, HealthProbeBeforeDisable, action)
	require.Contains(t, reason, "502")
}

func TestEvaluateChannelHealth_rechargeHighConfidence(t *testing.T) {
	resetChannelHealthForTest()
	ch := types.ChannelError{ChannelId: 25, ChannelName: "zxai", AutoBan: true}
	commonBackup := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() {
		common.AutomaticDisableChannelEnabled = commonBackup
		resetChannelHealthForTest()
	})

	err := types.NewErrorWithStatusCode(
		types.NewError(nil, types.ErrorCodeBadResponseStatusCode),
		types.ErrorCodeBadResponseStatusCode,
		403,
	)
	err.SetMessage("status_code=403, 余额不足")
	action, reason := EvaluateChannelHealth(ch, err)
	require.Equal(t, HealthNotifyRecharge, action)
	require.Contains(t, reason, "余额不足")
}

func TestEvaluateChannelHealth_upstreamBudgetExceeded(t *testing.T) {
	commonBackup := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() {
		common.AutomaticDisableChannelEnabled = commonBackup
		resetChannelHealthForTest()
	})

	messages := []string{
		"Model-level budget exceeded (virtual key scope): Model:AllModels:virtual_key:507da648 budget exceeded: 200.2732 >= 200.0000 dollars",
		"Virtual key abc has budget exceeded its configured limit",
		"virtual_key abc budget exceeded",
	}
	for i, message := range messages {
		t.Run(message, func(t *testing.T) {
			resetChannelHealthForTest()
			err := types.NewErrorWithStatusCode(
				types.NewError(nil, types.ErrorCodeBadResponseStatusCode),
				types.ErrorCodeBadResponseStatusCode,
				402,
			)
			err.SetMessage(message)

			require.Equal(t, CategoryUpstreamRecharge, ClassifyChannelError(err))
			require.True(t, IsHighConfidenceRecharge(err))
			action, reason := EvaluateChannelHealth(types.ChannelError{ChannelId: 100 + i, AutoBan: true}, err)
			require.Equal(t, HealthNotifyRecharge, action)
			require.Contains(t, reason, "上游账户欠费/额度不足")
		})
	}
}

func TestClassifyChannelError_genericBudgetExceededDoesNotDisable(t *testing.T) {
	t.Parallel()
	err := types.NewErrorWithStatusCode(
		types.NewError(nil, types.ErrorCodeBadResponseStatusCode),
		types.ErrorCodeBadResponseStatusCode,
		402,
	)
	err.SetMessage("request budget exceeded")

	require.Equal(t, CategorySkip, ClassifyChannelError(err))
	require.False(t, IsHighConfidenceRecharge(err))
}

func TestEvaluateChannelHealth_wrappedPlatformUserQuotaNeverDisables(t *testing.T) {
	resetChannelHealthForTest()
	ch := types.ChannelError{ChannelId: 68, ChannelName: "Apimart_原价", AutoBan: true}
	commonBackup := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() {
		common.AutomaticDisableChannelEnabled = commonBackup
		resetChannelHealthForTest()
	})

	makeErr := func() *types.NewAPIError {
		err := types.NewErrorWithStatusCode(
			types.NewError(nil, types.ErrorCodeBadResponseStatusCode),
			types.ErrorCodeBadResponseStatusCode,
			403,
		)
		err.SetMessage("status_code=403, 用户额度不足, 剩余额度: ＄0.000000")
		return err
	}

	for i := 0; i < 5; i++ {
		action, reason := EvaluateChannelHealth(ch, makeErr())
		require.Equal(t, HealthSkip, action, "attempt %d should skip", i+1)
		require.Empty(t, reason)
	}
}
