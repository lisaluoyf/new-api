package types

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRelayErrorCategories(t *testing.T) {
	for _, tc := range []struct {
		code, message string
		status        int
		want          string
	}{
		{"unsupported_reasoning_effort", "none", 400, "request_parameter"},
		{"gpt_subscription_rolling_limit", "GPT Pass", 403, "quota_limit"},
		{"unknown_error", "内容审计命中风险规则", 403, "content_policy"},
		{"client_canceled", "context canceled", 499, "client_disconnect"},
		{"unknown_error", "timeout", 524, "upstream_failure"},
		{"insufficient_quota", "upstream quota exceeded", 429, "upstream_rate_limit"},
	} {
		require.Equal(t, tc.want, RelayErrorCategory(tc.code, tc.message, tc.status))
	}
}
