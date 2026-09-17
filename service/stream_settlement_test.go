package service

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

func TestCompletedStreamSettlesAfterCancellation(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		name := "preconsumed"
		if trusted {
			name = "trusted"
		}
		t.Run(name, func(t *testing.T) {
			truncate(t)
			initial := common.GetTrustQuota() + 10000
			seedUser(t, 1, initial)
			seedToken(t, 2, 1, "retry-billing-key", initial)
			require.NoError(t, model.DB.Create(&model.Channel{Id: 94, Name: "settlement-test"}).Error)
			c := retryBillingContext()
			c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			ctx, cancel := context.WithCancel(c.Request.Context())
			defer cancel()
			c.Request = c.Request.WithContext(ctx)
			c.Set("token_quota", initial)
			c.Set(common.RequestIdKey, "settlement-"+name)
			info := retryBillingInfo(initial)
			info.RequestId = "settlement-" + name
			info.ForcePreConsume = !trusted
			info.StartTime = time.Now()
			info.IsStream = true
			info.RelayFormat = types.RelayFormatOpenAIResponses
			info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: 94, UpstreamModelName: info.OriginModelName}
			info.SetWalletPriceData(types.PriceData{ModelRatio: 1, CompletionRatio: 2, CacheRatio: 0.1, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}})
			info.ActivateWalletPriceData()
			require.Nil(t, PreConsumeBilling(c, 1000, info))
			if trusted {
				require.Zero(t, info.Billing.GetPreConsumedQuota())
			} else {
				require.Equal(t, 1000, info.Billing.GetPreConsumedQuota())
			}
			info.StreamStatus = relaycommon.NewStreamStatus()
			info.StreamStatus.RecordTerminalUsage("response.completed", "resp_test")
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, context.Canceled)
			cancel()
			usage := &dto.Usage{PromptTokens: 120, CompletionTokens: 7, TotalTokens: 127}
			usage.PromptTokensDetails.CachedTokens = 100
			PostTextConsumeQuota(c, info, usage, nil)
			// A late cleanup cannot refund settled usage; settling again cannot
			// debit either the wallet or token a second time.
			require.NoError(t, SettleBilling(c, info, 44))
			info.Billing.Refund(c)
			var user model.User
			var token model.Token
			require.NoError(t, model.DB.First(&user, 1).Error)
			require.NoError(t, model.DB.First(&token, 2).Error)
			require.Equal(t, initial-44, user.Quota)
			require.Equal(t, initial-44, token.RemainQuota)
			require.Equal(t, 1, user.RequestCount)
			var logs []model.Log
			require.NoError(t, model.LOG_DB.Where("request_id = ?", info.RequestId).Find(&logs).Error)
			require.Len(t, logs, 1)
			require.Equal(t, model.LogTypeConsume, logs[0].Type)
			require.Equal(t, 44, logs[0].Quota)
			require.Contains(t, logs[0].Other, "upstream_terminal")
		})
	}
}
