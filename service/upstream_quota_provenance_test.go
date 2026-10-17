package service

import (
	"errors"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestUpstreamQuotaProvenance(t *testing.T) {
	for _, message := range []string{"用户额度不足, 剩余额度: ＄-1.961192", "insufficient user quota", "token quota is not enough", "user quota is not enough"} {
		for _, code := range []types.ErrorCode{types.ErrorCodeBadResponseStatusCode, types.ErrorCodeInsufficientUserQuota} {
			upstream := types.WithOpenAIError(types.OpenAIError{Message: message, Type: "new_api_error", Code: string(code)}, 403)
			require.False(t, isPlatformUserQuotaError(upstream))
			require.Equal(t, CategoryUpstreamRecharge, ClassifyChannelError(upstream))
			require.True(t, IsHighConfidenceRecharge(upstream))
			wrapped := types.NewErrorWithStatusCode(errors.New(message), code, 403)
			wrapped.UpstreamResponseBody = `{"error":{"message":"用户额度不足","type":"new_api_error"}}`
			require.False(t, isPlatformUserQuotaError(wrapped))
			require.Equal(t, CategoryUpstreamRecharge, ClassifyChannelError(wrapped))
			local := types.NewErrorWithStatusCode(errors.New(message), code, 403)
			require.True(t, isPlatformUserQuotaError(local))
			require.Equal(t, CategorySkip, ClassifyChannelError(local))
			require.False(t, IsHighConfidenceRecharge(local))
		}
	}
}
