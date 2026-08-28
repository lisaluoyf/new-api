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

func seedGPTTrialBillingInfo(modelName string, pref string) *relaycommon.RelayInfo {
	info := &relaycommon.RelayInfo{
		RequestId:       "gpt-trial-billing-test-" + modelName + "-" + pref,
		UserId:          1,
		TokenId:         2,
		TokenKey:        "trial-billing-key",
		TokenGroup:      "default",
		UsingGroup:      "default",
		OriginModelName: modelName,
		IsPlayground:    true,
		UserSetting: dto.UserSetting{
			BillingPreference: pref,
		},
		GPTTrialChecked:   true,
		HasActiveGPTTrial: true,
	}
	info.SetWalletPriceData(types.PriceData{
		ModelRatio:        3,
		CompletionRatio:   2,
		QuotaToPreConsume: 120,
		GroupRatioInfo:    types.GroupRatioInfo{GroupRatio: 1.2},
	})
	info.SetTrialPriceData(types.PriceData{
		ModelRatio:        0.625,
		CompletionRatio:   8,
		QuotaToPreConsume: 40,
		GroupRatioInfo:    types.GroupRatioInfo{GroupRatio: 1},
	})
	info.ActivateWalletPriceData()
	return info
}

func TestPreConsumeBillingPrefersGPTTrialOverWalletPreference(t *testing.T) {
	truncate(t)
	seedUser(t, 1, 1000)
	seedToken(t, 2, 1, "trial-billing-key", 1000)
	seedSubscriptionPlan(t, 101, "APIMaster $20 GPT Trial", model.SubscriptionPlanTypeGPTTrial)
	seedUserSubscriptionWithPlan(t, 201, 1, 101, 500, 0)

	info := seedGPTTrialBillingInfo("gpt-5", "wallet_first")
	apiErr := PreConsumeBilling(retryBillingContext(), info.WalletPriceData.QuotaToPreConsume, info)
	require.Nil(t, apiErr)
	require.NotNil(t, info.Billing)
	require.Equal(t, BillingSourceSubscription, info.BillingSource)
	require.Equal(t, "gpt_trial", info.PriceDataSource)
	require.Equal(t, 40, info.Billing.GetPreConsumedQuota())
	require.InDelta(t, 0.625, info.PriceData.ModelRatio, 0.000001)
	require.InDelta(t, 1.0, info.PriceData.GroupRatioInfo.GroupRatio, 0.000001)

	var user model.User
	require.NoError(t, model.DB.First(&user, 1).Error)
	require.Equal(t, 1000, user.Quota)
}

func TestPreConsumeBillingIgnoresGPTTrialForNonEligibleModel(t *testing.T) {
	truncate(t)
	seedUser(t, 1, 1000)
	seedToken(t, 2, 1, "trial-billing-key", 1000)
	seedSubscriptionPlan(t, 101, "APIMaster $20 GPT Trial", model.SubscriptionPlanTypeGPTTrial)
	seedUserSubscriptionWithPlan(t, 201, 1, 101, 500, 0)

	info := seedGPTTrialBillingInfo("claude-sonnet-5", "wallet_first")
	apiErr := PreConsumeBilling(retryBillingContext(), info.WalletPriceData.QuotaToPreConsume, info)
	require.Nil(t, apiErr)
	require.NotNil(t, info.Billing)
	require.Equal(t, BillingSourceWallet, info.BillingSource)
	require.Equal(t, BillingSourceWallet, info.PriceDataSource)
	require.Equal(t, 120, info.Billing.GetPreConsumedQuota())
	require.InDelta(t, 3.0, info.PriceData.ModelRatio, 0.000001)

	var user model.User
	require.NoError(t, model.DB.First(&user, 1).Error)
	require.Equal(t, 880, user.Quota)
}

func TestPreConsumeBillingFallsBackToStandardSubscriptionAfterTrialInsufficient(t *testing.T) {
	truncate(t)
	seedUser(t, 1, 0)
	seedToken(t, 2, 1, "trial-billing-key", 1000)
	seedSubscriptionPlan(t, 101, "APIMaster $20 GPT Trial", model.SubscriptionPlanTypeGPTTrial)
	seedSubscriptionPlan(t, 102, "APIMaster Pro", model.SubscriptionPlanTypeStandard)
	seedUserSubscriptionWithPlan(t, 201, 1, 101, 20, 20)
	seedUserSubscriptionWithPlan(t, 202, 1, 102, 500, 0)

	info := seedGPTTrialBillingInfo("gpt-5", "subscription_first")
	apiErr := PreConsumeBilling(retryBillingContext(), info.WalletPriceData.QuotaToPreConsume, info)
	require.Nil(t, apiErr)
	require.NotNil(t, info.Billing)
	require.Equal(t, BillingSourceSubscription, info.BillingSource)
	require.Equal(t, BillingSourceWallet, info.PriceDataSource)
	require.Equal(t, 120, info.Billing.GetPreConsumedQuota())
	require.Equal(t, 102, info.SubscriptionPlanId)
}

func TestPreConsumeBillingUsesReferralRewardAfterTrial(t *testing.T) {
	truncate(t)
	seedUser(t, 1, 1000)
	seedToken(t, 2, 1, "trial-billing-key", 1000)
	seedSubscriptionPlan(t, 101, "APIMaster $50 GPT Trial", model.SubscriptionPlanTypeGPTTrial)
	seedSubscriptionPlan(t, 102, "APIMaster Referral GPT Credits", model.SubscriptionPlanTypeGPTReferralReward)
	seedUserSubscriptionWithPlan(t, 201, 1, 101, 40, 40)
	seedUserSubscriptionWithPlan(t, 202, 1, 102, 500, 0)

	info := seedGPTTrialBillingInfo("gpt-5", "wallet_first")
	info.HasActiveGPTReferralReward = true
	apiErr := PreConsumeBilling(retryBillingContext(), info.WalletPriceData.QuotaToPreConsume, info)
	require.Nil(t, apiErr)
	require.Equal(t, BillingSourceSubscription, info.BillingSource)
	require.Equal(t, model.SubscriptionPlanTypeGPTReferralReward, info.PriceDataSource)
	require.Equal(t, 40, info.Billing.GetPreConsumedQuota())
	require.Equal(t, 102, info.SubscriptionPlanId)
	require.InDelta(t, 0.625, info.PriceData.ModelRatio, 0.000001)

	var user model.User
	require.NoError(t, model.DB.First(&user, 1).Error)
	require.Equal(t, 1000, user.Quota)
}

func TestPreConsumeBillingUsesPaidGPTAfterTrialAndReferral(t *testing.T) {
	truncate(t)
	seedUser(t, 1, 1000)
	seedToken(t, 2, 1, "trial-billing-key", 1000)
	seedSubscriptionPlan(t, 101, "APIMaster $50 GPT Trial", model.SubscriptionPlanTypeGPTTrial)
	seedSubscriptionPlan(t, 102, "APIMaster Referral GPT Credits", model.SubscriptionPlanTypeGPTReferralReward)
	seedSubscriptionPlan(t, 103, "Pro", model.SubscriptionPlanTypeGPTSubscription)
	require.NoError(t, model.DB.Model(&model.SubscriptionPlan{}).Where("id = ?", 103).Updates(map[string]any{
		"model_allowlist": "gpt-5", "five_hour_amount": 500, "seven_day_amount": 1000,
	}).Error)
	model.InvalidateSubscriptionPlanCache(103)
	seedUserSubscriptionWithPlan(t, 201, 1, 101, 40, 40)
	seedUserSubscriptionWithPlan(t, 202, 1, 102, 40, 40)
	seedUserSubscriptionWithPlan(t, 203, 1, 103, 0, 0)
	require.NoError(t, model.DB.Model(&model.UserSubscription{}).Where("id = ?", 203).Updates(map[string]any{
		"five_hour_amount": 500, "seven_day_amount": 1000,
	}).Error)

	info := seedGPTTrialBillingInfo("gpt-5", "wallet_first")
	info.HasActiveGPTReferralReward = true
	info.HasActiveGPTSubscription = true
	apiErr := PreConsumeBilling(retryBillingContext(), info.WalletPriceData.QuotaToPreConsume, info)
	require.Nil(t, apiErr)
	require.Equal(t, BillingSourceSubscription, info.BillingSource)
	require.Equal(t, model.SubscriptionPlanTypeGPTSubscription, info.PriceDataSource)
	require.Equal(t, 103, info.SubscriptionPlanId)
	require.Equal(t, 40, info.Billing.GetPreConsumedQuota())

	var user model.User
	require.NoError(t, model.DB.First(&user, 1).Error)
	require.Equal(t, 1000, user.Quota)
}

func seedCodingPlanBilling(t *testing.T, planID, subscriptionID, userID int, modelName string, amountTotal, amountUsed int64) {
	t.Helper()
	seedSubscriptionPlan(t, planID, "Coding Plan", model.SubscriptionPlanTypeCodingPlan)
	require.NoError(t, model.DB.Model(&model.SubscriptionPlan{}).Where("id = ?", planID).Updates(map[string]any{
		"duration_value": 30, "coding_official_amount_usd": 79,
		"coding_model_multipliers": `{"` + modelName + `":0.350}`,
	}).Error)
	model.InvalidateSubscriptionPlanCache(planID)
	seedUserSubscriptionWithPlan(t, subscriptionID, userID, planID, amountTotal, amountUsed)
}

func addCodingPlanBillingInfo(info *relaycommon.RelayInfo, quota int) {
	info.HasActiveCodingPlan = true
	info.SetCodingPriceData(types.PriceData{
		ModelRatio:        0.35,
		CompletionRatio:   2,
		QuotaToPreConsume: quota,
		GroupRatioInfo:    types.GroupRatioInfo{GroupRatio: 1},
	})
}

func TestPreConsumeBillingPrefersPaidGPTOverCodingPlan(t *testing.T) {
	truncate(t)
	seedUser(t, 1, 1000)
	seedToken(t, 2, 1, "trial-billing-key", 1000)
	seedSubscriptionPlan(t, 103, "Pro", model.SubscriptionPlanTypeGPTSubscription)
	require.NoError(t, model.DB.Model(&model.SubscriptionPlan{}).Where("id = ?", 103).Updates(map[string]any{
		"model_allowlist": "gpt-5", "five_hour_amount": 500, "seven_day_amount": 1000,
	}).Error)
	model.InvalidateSubscriptionPlanCache(103)
	seedUserSubscriptionWithPlan(t, 203, 1, 103, 0, 0)
	require.NoError(t, model.DB.Model(&model.UserSubscription{}).Where("id = ?", 203).Updates(map[string]any{
		"five_hour_amount": 500, "seven_day_amount": 1000,
	}).Error)
	seedCodingPlanBilling(t, 104, 204, 1, "gpt-5", 1000, 0)

	info := seedGPTTrialBillingInfo("gpt-5", "wallet_first")
	info.HasActiveGPTTrial = false
	info.HasActiveGPTSubscription = true
	addCodingPlanBillingInfo(info, 25)
	require.Nil(t, PreConsumeBilling(retryBillingContext(), info.WalletPriceData.QuotaToPreConsume, info))
	require.Equal(t, model.SubscriptionPlanTypeGPTSubscription, info.PriceDataSource)
	require.Equal(t, 103, info.SubscriptionPlanId)
	require.Equal(t, 40, info.Billing.GetPreConsumedQuota())
}

func TestPreConsumeBillingUsesCodingPlanAfterPaidGPTRollingLimit(t *testing.T) {
	truncate(t)
	seedUser(t, 1, 1000)
	seedToken(t, 2, 1, "trial-billing-key", 1000)
	seedSubscriptionPlan(t, 103, "Pro", model.SubscriptionPlanTypeGPTSubscription)
	require.NoError(t, model.DB.Model(&model.SubscriptionPlan{}).Where("id = ?", 103).Updates(map[string]any{
		"model_allowlist": "gpt-5", "five_hour_amount": 30, "seven_day_amount": 100,
	}).Error)
	model.InvalidateSubscriptionPlanCache(103)
	seedUserSubscriptionWithPlan(t, 203, 1, 103, 0, 0)
	require.NoError(t, model.DB.Model(&model.UserSubscription{}).Where("id = ?", 203).Updates(map[string]any{
		"five_hour_amount": 30, "seven_day_amount": 100,
	}).Error)
	seedCodingPlanBilling(t, 104, 204, 1, "gpt-5", 1000, 0)

	info := seedGPTTrialBillingInfo("gpt-5", "wallet_first")
	info.HasActiveGPTTrial = false
	info.HasActiveGPTSubscription = true
	addCodingPlanBillingInfo(info, 25)
	require.Nil(t, PreConsumeBilling(retryBillingContext(), info.WalletPriceData.QuotaToPreConsume, info))
	require.Equal(t, model.SubscriptionPlanTypeCodingPlan, info.PriceDataSource)
	require.Equal(t, 104, info.SubscriptionPlanId)
	require.Equal(t, 25, info.Billing.GetPreConsumedQuota())

	var user model.User
	require.NoError(t, model.DB.First(&user, 1).Error)
	require.Equal(t, 1000, user.Quota)
}

func TestPreConsumeBillingFallsBackToWalletWhenCodingPlanDoesNotSupportModel(t *testing.T) {
	truncate(t)
	seedUser(t, 1, 1000)
	seedToken(t, 2, 1, "trial-billing-key", 1000)
	seedCodingPlanBilling(t, 104, 204, 1, "kimi-k3", 1000, 0)

	info := seedGPTTrialBillingInfo("gpt-5", "subscription_first")
	info.HasActiveGPTTrial = false
	addCodingPlanBillingInfo(info, 25)
	require.Nil(t, PreConsumeBilling(retryBillingContext(), info.WalletPriceData.QuotaToPreConsume, info))
	require.Equal(t, BillingSourceWallet, info.BillingSource)
	require.Equal(t, BillingSourceWallet, info.PriceDataSource)
	require.Equal(t, 120, info.Billing.GetPreConsumedQuota())

	var user model.User
	require.NoError(t, model.DB.First(&user, 1).Error)
	require.Equal(t, 880, user.Quota)
}

func TestPreConsumeBillingFallsBackToWalletWhenPaidRollingLimitReached(t *testing.T) {
	truncate(t)
	seedUser(t, 1, 1000)
	seedToken(t, 2, 1, "trial-billing-key", 1000)
	seedSubscriptionPlan(t, 103, "Pro", model.SubscriptionPlanTypeGPTSubscription)
	require.NoError(t, model.DB.Model(&model.SubscriptionPlan{}).Where("id = ?", 103).Updates(map[string]any{
		"model_allowlist": "gpt-5", "five_hour_amount": 30, "seven_day_amount": 100,
	}).Error)
	model.InvalidateSubscriptionPlanCache(103)
	seedUserSubscriptionWithPlan(t, 203, 1, 103, 0, 0)
	require.NoError(t, model.DB.Model(&model.UserSubscription{}).Where("id = ?", 203).Updates(map[string]any{
		"five_hour_amount": 30, "seven_day_amount": 100,
	}).Error)

	info := seedGPTTrialBillingInfo("gpt-5", "subscription_first")
	info.HasActiveGPTTrial = false
	info.HasActiveGPTSubscription = true
	apiErr := PreConsumeBilling(retryBillingContext(), info.WalletPriceData.QuotaToPreConsume, info)
	require.Nil(t, apiErr)
	require.Equal(t, BillingSourceWallet, info.BillingSource)
	var user model.User
	require.NoError(t, model.DB.First(&user, 1).Error)
	require.Equal(t, 880, user.Quota)
}

func TestPreConsumeBillingReturnsRollingRecoveryWhenWalletIsInsufficient(t *testing.T) {
	truncate(t)
	seedUser(t, 1, 0)
	seedToken(t, 2, 1, "trial-billing-key", 1000)
	seedSubscriptionPlan(t, 103, "Pro", model.SubscriptionPlanTypeGPTSubscription)
	require.NoError(t, model.DB.Model(&model.SubscriptionPlan{}).Where("id = ?", 103).Updates(map[string]any{
		"model_allowlist": "gpt-5", "five_hour_amount": 50, "seven_day_amount": 100,
	}).Error)
	model.InvalidateSubscriptionPlanCache(103)
	seedUserSubscriptionWithPlan(t, 203, 1, 103, 0, 0)
	require.NoError(t, model.DB.Model(&model.UserSubscription{}).Where("id = ?", 203).Updates(map[string]any{
		"five_hour_amount": 50, "seven_day_amount": 100,
	}).Error)
	record := model.SubscriptionPreConsumeRecord{
		RequestId: "paid-rolling-existing", UserId: 1, UserSubscriptionId: 203,
		PreConsumed: 30, PlanType: model.SubscriptionPlanTypeGPTSubscription, Status: "consumed",
	}
	require.NoError(t, model.DB.Create(&record).Error)
	createdAt := time.Now().Unix() - 3600
	require.NoError(t, model.DB.Model(&model.SubscriptionPreConsumeRecord{}).Where("id = ?", record.Id).Updates(map[string]any{
		"created_at": createdAt, "updated_at": createdAt,
	}).Error)

	info := seedGPTTrialBillingInfo("gpt-5", "subscription_first")
	info.HasActiveGPTTrial = false
	info.HasActiveGPTSubscription = true
	info.UserSetting.Language = "zh-CN"
	info.UserSetting.Timezone = "Europe/Moscow"
	apiErr := PreConsumeBilling(retryBillingContext(), info.WalletPriceData.QuotaToPreConsume, info)
	require.NotNil(t, apiErr)
	require.Equal(t, types.ErrorCodeGPTSubscriptionRollingLimit, apiErr.GetErrorCode())
	require.Contains(t, apiErr.Error(), "5 小时滚动额度已用尽")
	require.Contains(t, apiErr.Error(), "Europe/Moscow")
	require.Contains(t, apiErr.Error(), "钱包余额不足")
	var metadata map[string]any
	require.NoError(t, common.Unmarshal(apiErr.Metadata, &metadata))
	require.Equal(t, "gpt_subscription_rolling_limit", metadata["reason"])
	require.Equal(t, "Europe/Moscow", metadata["timezone"])
	require.Greater(t, int64(metadata["available_at"].(float64)), time.Now().Unix())
}

func TestPreConsumeBillingPrefersExpiringTrialOverReferralReward(t *testing.T) {
	truncate(t)
	seedUser(t, 1, 1000)
	seedToken(t, 2, 1, "trial-billing-key", 1000)
	seedSubscriptionPlan(t, 101, "APIMaster $50 GPT Trial", model.SubscriptionPlanTypeGPTTrial)
	seedSubscriptionPlan(t, 102, "APIMaster Referral GPT Credits", model.SubscriptionPlanTypeGPTReferralReward)
	seedUserSubscriptionWithPlan(t, 201, 1, 101, 500, 0)
	seedUserSubscriptionWithPlan(t, 202, 1, 102, 500, 0)

	info := seedGPTTrialBillingInfo("gpt-5", "wallet_first")
	info.HasActiveGPTReferralReward = true
	apiErr := PreConsumeBilling(retryBillingContext(), info.WalletPriceData.QuotaToPreConsume, info)
	require.Nil(t, apiErr)
	require.Equal(t, model.SubscriptionPlanTypeGPTTrial, info.PriceDataSource)
	require.Equal(t, 101, info.SubscriptionPlanId)
}

func TestPreConsumeBillingIgnoresReferralRewardForNonGPTModel(t *testing.T) {
	truncate(t)
	seedUser(t, 1, 1000)
	seedToken(t, 2, 1, "trial-billing-key", 1000)
	seedSubscriptionPlan(t, 102, "APIMaster Referral GPT Credits", model.SubscriptionPlanTypeGPTReferralReward)
	seedUserSubscriptionWithPlan(t, 202, 1, 102, 500, 0)

	info := seedGPTTrialBillingInfo("claude-sonnet-5", "wallet_first")
	info.HasActiveGPTTrial = false
	info.HasActiveGPTReferralReward = true
	apiErr := PreConsumeBilling(retryBillingContext(), info.WalletPriceData.QuotaToPreConsume, info)
	require.Nil(t, apiErr)
	require.Equal(t, BillingSourceWallet, info.BillingSource)
	require.Equal(t, BillingSourceWallet, info.PriceDataSource)
	require.Equal(t, 120, info.Billing.GetPreConsumedQuota())
}
