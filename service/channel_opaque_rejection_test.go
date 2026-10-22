package service

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

func TestOpaqueUpstreamRejectionBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, message, kind, param string
		code                       any
		status                     int
		want                       bool
	}{
		{"observed refusal", "请求被拒绝", "invalid_request_error", "", nil, 400, true},
		{"generic code", "请求被拒绝", "invalid_request_error", "", "unknown_error", 400, true},
		{"punctuation", " 请求被拒绝。\n", "invalid_request_error", "", "", 400, true},
		{"actionable parameter", "请求被拒绝", "invalid_request_error", "temperature", nil, 400, false},
		{"context", "请求被拒绝", "invalid_request_error", "", "context_length_exceeded", 400, false},
		{"tool result", "请求被拒绝", "invalid_request_error", "", "function_call_output_not_found", 400, false},
		{"policy code", "请求被拒绝", "invalid_request_error", "", "cyber_policy", 400, false},
		{"policy type", "请求被拒绝", "content_filter", "", nil, 400, false},
		{"quoted diagnosis", "Invalid parameter: 请求被拒绝", "invalid_request_error", "", nil, 400, false},
		{"context diagnosis", "请求被拒绝：上下文长度超限", "invalid_request_error", "", nil, 400, false},
		{"policy diagnosis", "请求被拒绝：内容安全审核未通过", "invalid_request_error", "", nil, 400, false},
		{"different status", "请求被拒绝", "invalid_request_error", "", nil, 403, false},
		{"unobserved English", "Request rejected", "invalid_request_error", "", nil, 400, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := types.WithOpenAIError(types.OpenAIError{Message: tc.message, Type: tc.kind, Param: tc.param, Code: tc.code}, tc.status)
			require.Equal(t, tc.want, IsOpaqueUpstreamRejection(err))
			want := CategorySkip
			if tc.want {
				want = CategoryDisableWindow
			}
			require.Equal(t, want, ClassifyChannelError(err))
		})
	}
	require.False(t, IsOpaqueUpstreamRejection(nil))
	local := types.NewErrorWithStatusCode(errors.New("请求被拒绝"), types.ErrorCodeInvalidRequest, 400)
	require.False(t, IsOpaqueUpstreamRejection(local))
	for _, option := range []types.NewAPIErrorOptions{types.ErrOptionWithSkipRetry()} {
		err := types.WithOpenAIError(types.OpenAIError{Message: "请求被拒绝", Type: "invalid_request_error"}, 400, option)
		require.False(t, IsOpaqueUpstreamRejection(err))
	}
	err := types.WithOpenAIError(types.OpenAIError{Message: "请求被拒绝", Type: "invalid_request_error", Metadata: []byte(`{"reason":"content_filter"}`)}, 400)
	require.False(t, IsOpaqueUpstreamRejection(err))
}

func TestOpaqueRejectionUsesWindowProbeAndRecurringCooldown(t *testing.T) {
	old := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	resetChannelHealthForTest()
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = old; resetChannelHealthForTest() })
	err := types.WithOpenAIError(types.OpenAIError{Message: "请求被拒绝", Type: "invalid_request_error"}, 400)
	for _, id := range []int{82, 258, 262, 308} {
		ch := types.ChannelError{ChannelId: id, AutoBan: true}
		target := types.ChannelProbeTarget{ModelName: "gpt-6-astra", EndpointType: constant.EndpointTypeOpenAI}
		for episode := 0; episode < 3; episode++ {
			for i := 0; i < 5; i++ {
				RecordChannelSuccess(id, target)
			}
			for i := 0; i < 5; i++ {
				action, _ := EvaluateChannelHealth(ch, err, target)
				if i < 4 {
					require.Equal(t, HealthSkip, action)
				} else {
					require.Equal(t, HealthProbeBeforeDisable, action)
				}
			}
			require.Equal(t, episode == 2, RecordPassedDisableProbe(ch, err, target), "a healthy small probe must not erase recurring real-request failures")
			ClearChannelHealth(id, target)
		}
		ch.AutoBan = false
		action, _ := EvaluateChannelHealth(ch, err, target)
		require.Equal(t, HealthSkip, action)
	}
	common.AutomaticDisableChannelEnabled = false
	action, _ := EvaluateChannelHealth(types.ChannelError{ChannelId: 82, AutoBan: true}, err, types.ChannelProbeTarget{ModelName: "gpt-6-astra", EndpointType: constant.EndpointTypeOpenAI})
	require.Equal(t, HealthSkip, action)
}

func TestPolicyRejectionsWrappedAs502NeverBecomeHealthFaults(t *testing.T) {
	old := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	resetChannelHealthForTest()
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = old; resetChannelHealthForTest() })
	for _, id := range []int{82, 258, 262, 308} {
		ch := types.ChannelError{ChannelId: id, AutoBan: true}
		target := types.ChannelProbeTarget{ModelName: "gpt-6-astra", EndpointType: constant.EndpointTypeOpenAIResponse, IsStream: true}
		for _, policy := range []types.OpenAIError{
			{Code: "cyber_policy", Type: "invalid_request_error", Message: "The request was rejected by a content policy."},
			{Type: "invalid_request_error", Message: "This content was flagged for possible cybersecurity risk."},
			{Code: "content_policy_violation", Type: "invalid_request_error", Message: "Rejected"},
		} {
			err := types.WithOpenAIError(policy, 502)
			for i := 0; i < 20; i++ {
				require.Equal(t, CategorySkip, ClassifyChannelError(err))
				action, _ := EvaluateChannelHealth(ch, err, target)
				require.Equal(t, HealthSkip, action)
				require.False(t, RecordPassedDisableProbe(ch, err, target))
			}
		}
		// Only actual availability errors advance the window after policy refusals.
		operational := types.WithOpenAIError(types.OpenAIError{Type: "server_error", Message: "Content safety service unavailable"}, 503)
		require.Equal(t, CategoryDisableWindow, ClassifyChannelError(operational))
	}
}
