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

func TestCanceledClaudeUsageSettlesReportedCacheCountsOnce(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		t.Run(map[bool]string{false: "precharged", true: "trusted"}[trusted], func(t *testing.T) {
			setupCancellationObservationTest(t)
			initial := common.GetTrustQuota() + 10000
			seedUser(t, 1, initial)
			seedToken(t, 2, 1, "retry-billing-key", initial)
			require.NoError(t, model.DB.Create(&model.Channel{Id: 221, Name: "cancel-test"}).Error)
			c := retryBillingContext()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c.Request = httptest.NewRequest("POST", "/v1/messages", nil).WithContext(ctx)
			c.Set("channel_id", 221)
			c.Set("token_quota", initial)
			c.Set(common.RequestIdKey, "cancel-partial")
			info := retryBillingInfo(initial)
			info.RequestId = "cancel-partial"
			info.ForcePreConsume = !trusted
			info.StartTime = time.Now()
			info.IsStream = true
			info.RelayFormat = types.RelayFormatClaude
			info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: 221, UpstreamModelName: info.OriginModelName}
			info.SetWalletPriceData(types.PriceData{ModelRatio: 1, CompletionRatio: 5, CacheRatio: .1, CacheCreationRatio: 1.25, CacheCreation5mRatio: 1.25, CacheCreation1hRatio: 2, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}})
			info.ActivateWalletPriceData()
			require.Nil(t, PreConsumeBilling(c, 1000, info))
			usage := &dto.Usage{PromptTokens: 100, CompletionTokens: 1, UsageSemantic: "anthropic", UsageSource: "upstream_reported_partial", PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 200, CachedCreationTokens: 30}, ClaudeCacheCreation5mTokens: 10, ClaudeCacheCreation1hTokens: 20}
			info.CanceledStreamUsage = usage
			info.StreamStatus = relaycommon.NewStreamStatus()
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, context.Canceled)
			cancel()
			require.NoError(t, PostTextConsumeQuota(c, info, usage, nil))
			require.NoError(t, PostTextConsumeQuota(c, info, usage, nil))
			info.Billing.Refund(c)
			var user model.User
			var token model.Token
			require.NoError(t, model.DB.First(&user, 1).Error)
			require.NoError(t, model.DB.First(&token, 2).Error)
			// 100 + 1*5 + 200*.1 + 10*1.25 + 20*2 = 177.5 => 178.
			require.Equal(t, initial-178, user.Quota)
			require.Equal(t, initial-178, token.RemainQuota)
			require.Equal(t, 1, user.RequestCount)
			var logs []model.Log
			require.NoError(t, model.LOG_DB.Where("request_id = ?", info.RequestId).Find(&logs).Error)
			require.Len(t, logs, 1)
			require.Equal(t, 178, logs[0].Quota)
			require.Equal(t, model.LogTypeConsume, logs[0].Type)
			require.Contains(t, logs[0].Other, `"output_usage_complete":false`)
			observation, err := model.GetCancellationObservation(info.RequestId)
			require.NoError(t, err)
			require.Contains(t, observation.RequestSnapshot, `"reported_partial_usage"`)
		})
	}
}

func TestReportedCacheOnlyUsageIsBillable(t *testing.T) {
	c := retryBillingContext()
	info := retryBillingInfo(10000)
	info.StartTime = time.Now()
	info.PriceData = types.PriceData{ModelRatio: 1, CacheRatio: .1, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}}
	usage := &dto.Usage{UsageSemantic: "anthropic", UsageSource: "upstream_reported_partial", PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 200}}
	summary := calculateTextQuotaSummary(c, info, usage)
	require.Equal(t, 200, summary.TotalTokens)
	require.Equal(t, 20, summary.Quota)
}

func TestCanceledUsagePersistenceFailureDoesNotDebit(t *testing.T) {
	setupCancellationObservationTest(t)
	initial := common.GetTrustQuota() + 10000
	seedUser(t, 1, initial)
	seedToken(t, 2, 1, "retry-billing-key", initial)
	c := retryBillingContext()
	c.Set("token_quota", initial)
	c.Set("channel_id", 221)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	info := retryBillingInfo(initial)
	info.RequestId = "cancel-persist-failure"
	info.ForcePreConsume = true
	info.StartTime = time.Now()
	require.Nil(t, PreConsumeBilling(c, 1000, info))
	usage := &dto.Usage{PromptTokens: 100, UsageSemantic: "anthropic", UsageSource: "upstream_reported_partial"}
	info.CanceledStreamUsage = usage
	require.NoError(t, model.DB.Migrator().DropTable(&model.CancellationObservation{}))
	t.Cleanup(func() { require.NoError(t, model.DB.AutoMigrate(&model.CancellationObservation{})) })
	require.Error(t, PostTextConsumeQuota(c, info, usage, nil))
	require.NoError(t, info.Billing.RefundSync(c))
	var user model.User
	require.NoError(t, model.DB.First(&user, 1).Error)
	require.Equal(t, initial, user.Quota)
	require.Zero(t, user.RequestCount)
}
