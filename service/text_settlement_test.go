package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

func TestTextSettlementPendingDoesNotRefundOrRecordFalseConsumption(t *testing.T) {
	truncate(t)
	initial := common.GetTrustQuota() + 10000
	seedUser(t, 1, initial)
	seedToken(t, 2, 1, "retry-billing-key", initial)
	c := retryBillingContext()
	c.Set("token_quota", initial)
	c.Set(common.RequestIdKey, "durable-service-request")
	info := retryBillingInfo(initial)
	info.RequestId = "durable-service-request"
	info.StartTime = time.Now()
	info.ForcePreConsume = true
	info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: 94}
	require.NoError(t, model.DB.Create(&model.Channel{Id: 94}).Error)
	info.SetWalletPriceData(types.PriceData{ModelRatio: 1, CompletionRatio: 2, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}})
	info.ActivateWalletPriceData()
	require.Nil(t, PreConsumeBilling(c, 1000, info))
	// Another concurrent request exhausted the remaining wallet after precharge.
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 1).Update("quota", 0).Error)
	usage := &dto.Usage{PromptTokens: 2000, TotalTokens: 2000, UsageSource: "upstream_reported"}
	require.NoError(t, PostTextConsumeQuota(c, info, usage, nil))
	require.NoError(t, PostTextConsumeQuota(c, info, usage, nil))
	require.Error(t, SettleBilling(c, info, 2000))
	require.NoError(t, info.Billing.RefundSync(c))
	var user model.User
	require.NoError(t, model.DB.First(&user, 1).Error)
	require.Zero(t, user.Quota)
	require.Zero(t, user.RequestCount)
	var log model.Log
	require.NoError(t, model.LOG_DB.Where("request_id = ?", info.RequestId).First(&log).Error)
	require.Equal(t, model.LogTypeError, log.Type)
	require.Zero(t, log.Quota)
	require.Equal(t, 2000, log.PromptTokens)
	originalID := log.Id
	_, apiErr := NewBillingSession(c, retryBillingInfo(initial), 1000)
	require.NotNil(t, apiErr, "new paid calls must be blocked while usage is unpaid")
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 1).Update("quota", 5000).Error)
	require.NoError(t, model.RetryTextSettlements(common.GetTimestamp()+120))
	require.NoError(t, model.DB.First(&user, 1).Error)
	require.Equal(t, 4000, user.Quota)
	require.Equal(t, 2000, user.UsedQuota)
	require.Equal(t, 1, user.RequestCount)
	require.NoError(t, model.LOG_DB.Where("request_id = ?", info.RequestId).First(&log).Error)
	require.Equal(t, originalID, log.Id)
	require.Equal(t, model.LogTypeConsume, log.Type)
	require.Equal(t, 2000, log.Quota)
}

func TestNewBillingSessionSettlesFreeCreditOverflowAndUsesSameKeyWallet(t *testing.T) {
	truncate(t)
	seedUser(t, 1, 100)
	seedToken(t, 2, 1, "retry-billing-key", 100)
	require.NoError(t, model.DB.Create(&model.Channel{Id: 94}).Error)
	require.NoError(t, model.DB.Create(&model.SubscriptionPlan{Id: 8, Title: "Referral credits", PlanType: model.SubscriptionPlanTypeGPTReferralReward, PriceAmount: 0}).Error)
	require.NoError(t, model.DB.Create(&model.UserSubscription{Id: 7, UserId: 1, PlanId: 8, AmountTotal: 30, AmountUsed: 10, Status: "active", StartTime: time.Now().Add(-time.Hour).Unix(), EndTime: time.Now().Add(time.Hour).Unix()}).Error)
	item := &model.TextSettlement{RequestId: "free-overflow-before-next-call", UserId: 1, TokenId: 2, ChannelId: 94, FundingSource: BillingSourceSubscription, SubscriptionId: 7, PreConsumedQuota: 10, TokenConsumed: 10, Quota: 50}
	require.NoError(t, model.DB.Create(&model.SubscriptionPreConsumeRecord{RequestId: item.RequestId, UserId: 1, UserSubscriptionId: 7, PreConsumed: 10}).Error)
	require.NoError(t, model.FreezeTextSettlement(item, &model.Log{UserId: 1, RequestId: item.RequestId, Type: model.LogTypeConsume, Quota: 50, CreatedAt: common.GetTimestamp()}, model.AccountingLogFields{}))
	info := retryBillingInfo(100)
	info.RequestId = "next-call-same-key"
	info.UserSetting.BillingPreference = "subscription_first"
	info.ForcePreConsume = true
	session, apiErr := NewBillingSession(retryBillingContext(), info, 10)
	require.Nil(t, apiErr)
	require.Equal(t, BillingSourceWallet, session.funding.Source())
	require.Equal(t, 2, session.relayInfo.TokenId)
	require.Equal(t, "retry-billing-key", session.relayInfo.TokenKey)
	var stored model.TextSettlement
	require.NoError(t, model.DB.First(&stored, item.Id).Error)
	require.Equal(t, "settled", stored.Status)
	require.Equal(t, 20, stored.WalletSupplementQuota)
	require.NoError(t, session.RefundSync(retryBillingContext()))
	var user model.User
	require.NoError(t, model.DB.First(&user, 1).Error)
	require.Equal(t, 80, user.Quota)
}
