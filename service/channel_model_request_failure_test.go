package service

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

func TestModelRequestFailureRequiresIndependentProbe(t *testing.T) {
	previous := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = previous })
	for _, tc := range []struct {
		name, message, code string
		status              int
		want                ChannelErrorCategory
	}{
		{"observed relay failure", "模型请求失败，请稍后重试或更换模型。", "", 400, CategoryProbeBeforeDisable},
		{"without punctuation", "模型请求失败，请稍后重试或更换模型", "", 400, CategoryProbeBeforeDisable},
		{"surrounding whitespace", " 模型请求失败，请稍后重试或更换模型。\n", "", 400, CategoryProbeBeforeDisable},
		{"invalid parameter", "Invalid parameter: temperature", "", 400, CategorySkip},
		{"context overflow", "maximum context length exceeded", "context_length_exceeded", 400, CategorySkip},
		{"quoted diagnosis", "Invalid parameter: 模型请求失败，请稍后重试或更换模型。", "", 400, CategorySkip},
		{"different status", "模型请求失败，请稍后重试或更换模型。", "", 422, CategorySkip},
		{"user quota takes precedence", "模型请求失败，请稍后重试或更换模型。", string(types.ErrorCodeInsufficientUserQuota), 400, CategorySkip},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := types.WithOpenAIError(types.OpenAIError{Message: tc.message, Type: "invalid_request_error", Code: tc.code}, tc.status)
			err.UpstreamResponseBody = `{"error":{"message":"模型请求失败，请稍后重试或更换模型。","type":"invalid_request_error"}}`
			require.Equal(t, tc.want, ClassifyChannelError(err))
			channel := types.ChannelError{ChannelId: 216, AutoBan: true}
			action, _ := EvaluateChannelHealth(channel, err)
			if tc.want == CategoryProbeBeforeDisable {
				require.Equal(t, HealthProbeBeforeDisable, action)
				channel.AutoBan = false
				action, _ = EvaluateChannelHealth(channel, err)
				require.Equal(t, HealthSkip, action)
			} else {
				require.Equal(t, HealthSkip, action)
			}
		})
	}
	common.AutomaticDisableChannelEnabled = false
	err := types.WithOpenAIError(types.OpenAIError{Message: "模型请求失败，请稍后重试或更换模型。", Type: "invalid_request_error"}, http.StatusBadRequest)
	action, _ := EvaluateChannelHealth(types.ChannelError{ChannelId: 216, AutoBan: true}, err)
	require.Equal(t, HealthSkip, action)
}
