package service

import (
	"net/http"
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
