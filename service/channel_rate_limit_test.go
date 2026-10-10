package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestClassifyChannelErrorStructuredRateLimit(t *testing.T) {
	for _, tc := range []struct {
		name, message, errorType, code, body string
		status                               int
		want                                 ChannelErrorCategory
	}{
		{"luna original", "Luna model rate limit exceeded.", "rate_limit_error", "luna_rpm_exceeded", `{"error":{"code":"luna_rpm_exceeded","message":"Luna model rate limit exceeded.","type":"rate_limit_error"}}`, 429, CategoryRateLimitWindow},
		{"type only", "Try again later", "rate_limit_error", "other", "", 429, CategoryRateLimitWindow},
		{"code only", "Try again later", "upstream_error", "luna_rpm_exceeded", "", 429, CategoryRateLimitWindow},
		{"nested type", "Try again later", "upstream_error", "other", `{"error":{"type":"rate_limit_error","code":"other"}}`, 429, CategoryRateLimitWindow},
		{"top code", "Try again later", "upstream_error", "other", `{"code":"luna_rpm_exceeded"}`, 429, CategoryRateLimitWindow},
		{"message only", "Luna model rate limit exceeded.", "upstream_error", "other", "", 429, CategoryRateLimitWindow},
		{"unrelated 429", "Try again later", "upstream_error", "other", "", 429, CategorySkip},
		{"parameter mention", "Try again later", "upstream_error", "other", `{"param":"rate_limit_error","metadata":{"code":"luna_rpm_exceeded"}}`, 429, CategorySkip},
		{"non 429", "Try again later", "rate_limit_error", "luna_rpm_exceeded", "", 400, CategorySkip},
		{"user quota priority", "用户额度不足", "rate_limit_error", "luna_rpm_exceeded", "", 429, CategorySkip},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := types.WithOpenAIError(types.OpenAIError{Message: tc.message, Type: tc.errorType, Code: tc.code}, tc.status)
			err.UpstreamResponseBody = tc.body
			require.Equal(t, tc.want, ClassifyChannelError(err))
		})
	}
}

func TestEvaluateChannelHealthLunaRateLimit(t *testing.T) {
	resetChannelHealthForTest()
	previous := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = previous; resetChannelHealthForTest() })
	ch := types.ChannelError{ChannelId: 94, AutoBan: true}
	err := types.WithOpenAIError(types.OpenAIError{Message: "Luna model rate limit exceeded.", Type: "rate_limit_error", Code: "luna_rpm_exceeded"}, 429)
	for i := 0; i < 7; i++ {
		action, _ := EvaluateChannelHealth(ch, err)
		require.Equal(t, HealthSkip, action)
	}
	action, _ := EvaluateChannelHealth(ch, err)
	require.Equal(t, HealthProbeBeforeDisable, action)
	ch.AutoBan = false
	action, _ = EvaluateChannelHealth(ch, err)
	require.Equal(t, HealthSkip, action)
	ch.AutoBan = true
	common.AutomaticDisableChannelEnabled = false
	action, _ = EvaluateChannelHealth(ch, err)
	require.Equal(t, HealthSkip, action)
}
