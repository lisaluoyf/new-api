package controller

import (
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestTypeSafeChannelTestUsesNativeProtocol(t *testing.T) {
	ch := &model.Channel{Type: constant.ChannelTypeTypeSafe}
	endpoint := normalizeChannelTestEndpoint(ch, "jev-latest", "openai")
	require.Equal(t, "typesafe", endpoint)
	req := buildTestRequest("jev-latest", endpoint, ch, true).(*dto.TypeSafeRequest)
	require.NoError(t, req.Validate())
	require.False(t, req.IsStream(nil))
	require.Contains(t, fastTokenCountMetaForPricing(req).CombineText, "working")
}

func TestTypeSafeTestBillingIgnoresOutputTokens(t *testing.T) {
	price := types.PriceData{ModelRatio: 0.021, CompletionRatio: 0}
	for _, output := range []int{0, 20, 1000000} {
		quota, _ := settleTestQuota(&relaycommon.RelayInfo{}, price, &dto.Usage{PromptTokens: 1000000, CompletionTokens: output})
		require.Equal(t, 21000, quota) // $0.042 at 500,000 quota units/USD.
	}
}
