package service

import (
	"context"
	"math"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

func TestUserModelDiscountUsesExactModelAndWalletPriceData(t *testing.T) {
	setting := dto.UserSetting{ModelDiscountRatios: map[string]float64{"seedance-2.5": 0.9}}
	price := types.PriceData{
		Quota:             950,
		QuotaToPreConsume: 950,
		GroupRatioInfo:    types.GroupRatioInfo{GroupRatio: 0.95, GroupSpecialRatio: 0.95, HasSpecialRatio: true},
	}

	got := ApplyUserModelDiscountToPriceData(price, setting, "seedance-2.5")

	require.Equal(t, 855, got.Quota)
	require.Equal(t, 855, got.QuotaToPreConsume)
	require.InDelta(t, 0.855, got.GroupRatioInfo.GroupRatio, 1e-9)
	require.InDelta(t, 0.9, got.GroupRatioInfo.UserModelDiscount, 1e-9)
	require.InDelta(t, 0.95, got.GroupRatioInfo.GroupSpecialRatio, 1e-9)
	require.Equal(t, got, ApplyUserModelDiscountToPriceData(got, setting, "seedance-2.5"))
	require.Equal(t, price, ApplyUserModelDiscountToPriceData(price, setting, "seedance-2.5-fast"))
	require.InDelta(t, .95, UserModelDiscountLogGroupRatio(got.GroupRatioInfo), 1e-9)
}

func TestUserModelDiscountValidatesFiniteExactRules(t *testing.T) {
	for _, invalid := range []map[string]float64{
		{"": .9}, {"model": 0}, {"model": -1}, {"model": 1.1},
		{"model": math.NaN()}, {"model": math.Inf(1)},
		{" model ": .9, "model": .8}, {"model": .0000001},
	} {
		require.Error(t, ValidateUserModelDiscountRatios(invalid))
	}
	require.NoError(t, ValidateUserModelDiscountRatios(nil))
	require.NoError(t, ValidateUserModelDiscountRatios(map[string]float64{"seedance-2.5": .9 / .95}))
}

func TestUserModelDiscountWalletReservationRetrySettlementAndRefund(t *testing.T) {
	for _, refund := range []bool{false, true} {
		t.Run(map[bool]string{false: "settle", true: "refund"}[refund], func(t *testing.T) {
			truncate(t)
			seedUser(t, 1, 1000)
			seedToken(t, 2, 1, "retry-billing-key", 1000)
			info := retryBillingInfo(1000)
			info.ForcePreConsume = true
			info.UserSetting.ModelDiscountRatios = map[string]float64{info.OriginModelName: .9 / .95}
			info.SetWalletPriceData(types.PriceData{QuotaToPreConsume: 95, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: .95}})
			ctx := retryBillingContext()
			require.Nil(t, PreConsumeBilling(ctx, 95, info))
			require.Equal(t, 90, info.Billing.GetPreConsumedQuota())
			require.InDelta(t, .9, info.PriceData.GroupRatioInfo.GroupRatio, 1e-12)
			fallback := ApplyUserModelDiscountToPriceData(types.PriceData{QuotaToPreConsume: 285, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: .95}}, info.UserSetting, info.OriginModelName)
			require.Nil(t, PrepareRetryBilling(ctx, info, fallback))
			require.Equal(t, 270, info.Billing.GetPreConsumedQuota())
			if refund {
				session := info.Billing.(*BillingSession)
				require.NoError(t, session.RefundSync(ctx))
				require.NoError(t, session.RefundSync(ctx))
			} else {
				require.NoError(t, info.Billing.Settle(180))
				require.NoError(t, info.Billing.Settle(180))
				require.NoError(t, info.Billing.(*BillingSession).RefundSync(ctx))
			}
			expected := 820
			if refund {
				expected = 1000
			}
			var user model.User
			var token model.Token
			require.NoError(t, model.DB.First(&user, 1).Error)
			require.NoError(t, model.DB.First(&token, 2).Error)
			require.Equal(t, expected, user.Quota)
			require.Equal(t, expected, token.RemainQuota)
			require.Equal(t, 1000-expected, token.UsedQuota)
		})
	}
}

func TestUserModelDiscountDoesNotApplyToSubscriptionOrTrial(t *testing.T) {
	for _, trial := range []bool{false, true} {
		t.Run(map[bool]string{false: "standard", true: "trial"}[trial], func(t *testing.T) {
			truncate(t)
			seedUser(t, 1, 1000)
			seedToken(t, 2, 1, "trial-billing-key", 1000)
			planType := model.SubscriptionPlanTypeStandard
			if trial {
				planType = model.SubscriptionPlanTypeGPTTrial
			}
			seedSubscriptionPlan(t, 101, "discount-excluded", planType)
			seedUserSubscriptionWithPlan(t, 201, 1, 101, 500, 0)
			info := seedGPTTrialBillingInfo("gpt-5", "subscription_first")
			info.UserSetting.ModelDiscountRatios = map[string]float64{"gpt-5": .5}
			require.Nil(t, PreConsumeBilling(retryBillingContext(), 120, info))
			require.Equal(t, BillingSourceSubscription, info.BillingSource)
			require.Zero(t, info.PriceData.GroupRatioInfo.UserModelDiscount)
			expected := 120
			if trial {
				expected = 40
			}
			require.Equal(t, expected, info.Billing.GetPreConsumedQuota())
			require.Equal(t, 1000, getUserQuota(t, 1))
		})
	}
}

func TestUserModelDiscountTieredSnapshotLeavesBaseAndProcurementUntouched(t *testing.T) {
	setting := dto.UserSetting{ModelDiscountRatios: map[string]float64{"discount-tier": .9 / .95}}
	snapshot := &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprString: `tier("base", p * 2)`, QuotaPerUnit: 500000, GroupRatio: .95, EstimatedQuotaAfterGroup: 950}
	discounted := ApplyUserModelDiscountToBillingSnapshot(snapshot, setting, "discount-tier")
	require.Equal(t, 900, discounted.EstimatedQuotaAfterGroup)
	require.Equal(t, 950, snapshot.EstimatedQuotaAfterGroup)
	require.Equal(t, .95, snapshot.GroupRatio)
	info := &relaycommon.RelayInfo{TieredBillingSnapshot: discounted, WalletTieredBillingSnapshot: snapshot, ProcurementTieredBillingSnapshot: snapshot}
	ok, quota, _ := TryTieredSettle(info, billingexpr.TokenParams{P: 1000})
	require.True(t, ok)
	require.Equal(t, 900, quota)
}

func TestUserModelDiscountTextDebitLogAndAccountingAgree(t *testing.T) {
	truncate(t)
	require.NoError(t, model.DB.AutoMigrate(&model.ChannelModelPricing{}))
	seedUser(t, 1, 5000)
	seedToken(t, 2, 1, "retry-billing-key", 5000)
	seedChannel(t, 3)
	info := retryBillingInfo(5000)
	info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: 3}
	info.StartTime = time.Now()
	info.FirstResponseTime = info.StartTime
	info.ForcePreConsume = true
	info.UserSetting.ModelDiscountRatios = map[string]float64{info.OriginModelName: .9 / .95}
	info.SetWalletPriceData(types.PriceData{ModelRatio: 1, CompletionRatio: 1, QuotaToPreConsume: 950, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: .95, GroupSpecialRatio: -1}})
	ctx := retryBillingContext()
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	ctx.Set(common.RequestIdKey, info.RequestId)
	require.Nil(t, PreConsumeBilling(ctx, 950, info))
	usage := &dto.Usage{PromptTokens: 1000, TotalTokens: 1000}
	require.NoError(t, PostTextConsumeQuota(ctx, info, usage, nil))
	require.NoError(t, PostTextConsumeQuota(ctx, info, usage, nil))
	var user model.User
	var token model.Token
	var channel model.Channel
	var log model.Log
	require.NoError(t, model.DB.First(&user, 1).Error)
	require.NoError(t, model.DB.First(&token, 2).Error)
	require.NoError(t, model.DB.First(&channel, 3).Error)
	require.NoError(t, model.LOG_DB.Where("request_id = ? AND type = ?", info.RequestId, model.LogTypeConsume).First(&log).Error)
	require.Equal(t, 4100, user.Quota)
	require.Equal(t, 900, user.UsedQuota)
	require.Equal(t, 1, user.RequestCount)
	require.Equal(t, 4100, token.RemainQuota)
	require.Equal(t, 900, token.UsedQuota)
	require.EqualValues(t, 900, channel.UsedQuota)
	require.Equal(t, 900, log.Quota)
	require.InDelta(t, 900/common.QuotaPerUnit, log.AccountingUserFinalAmountUSD, 1e-12)
	other, err := common.StrToMap(log.Other)
	require.NoError(t, err)
	require.InDelta(t, .95, other["group_ratio"], 1e-12)
	require.InDelta(t, .9/.95, other["user_model_discount"], 1e-12)
}

func TestUserModelDiscountRealtimeReservationAndSettlement(t *testing.T) {
	for _, refund := range []bool{false, true} {
		t.Run(map[bool]string{false: "settle", true: "refund"}[refund], func(t *testing.T) {
			truncate(t)
			seedUser(t, 1, 50000)
			seedToken(t, 2, 1, "retry-billing-key", 50000)
			seedChannel(t, 3)
			info := retryBillingInfo(50000)
			info.OriginModelName = "gpt-4"
			info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: 3}
			info.UpstreamModelName = info.OriginModelName
			info.StartTime = time.Now()
			info.FirstResponseTime = info.StartTime
			info.ForcePreConsume = true
			info.UserSetting.ModelDiscountRatios = map[string]float64{info.OriginModelName: .9 / .95}
			info.SetWalletPriceData(types.PriceData{ModelRatio: 15, QuotaToPreConsume: 950, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: .95, GroupSpecialRatio: -1}})
			ctx := retryBillingContext()
			require.Nil(t, PreConsumeBilling(ctx, 950, info))
			usage := &dto.RealtimeUsage{InputTokens: 100, TotalTokens: 100}
			usage.InputTokenDetails.TextTokens = 100
			require.NoError(t, PreWssConsumeQuota(ctx, info, usage))
			require.Equal(t, 1350, info.Billing.GetPreConsumedQuota())
			require.NoError(t, PreWssConsumeQuota(ctx, info, usage))
			require.Equal(t, 2700, info.Billing.GetPreConsumedQuota())
			expected := 50000
			if refund {
				require.NoError(t, info.Billing.(*BillingSession).RefundSync(ctx))
				require.NoError(t, info.Billing.(*BillingSession).RefundSync(ctx))
			} else {
				total := &dto.RealtimeUsage{InputTokens: 200, TotalTokens: 200}
				total.InputTokenDetails.TextTokens = 200
				PostWssConsumeQuota(ctx, info, info.OriginModelName, total, "")
				expected -= 2700
				var log model.Log
				require.NoError(t, model.LOG_DB.Where("type = ?", model.LogTypeConsume).Order("id DESC").First(&log).Error)
				require.Equal(t, 2700, log.Quota)
				require.InDelta(t, 2700/common.QuotaPerUnit, log.AccountingUserFinalAmountUSD, 1e-12)
			}
			var user model.User
			var token model.Token
			require.NoError(t, model.DB.First(&user, 1).Error)
			require.NoError(t, model.DB.First(&token, 2).Error)
			require.Equal(t, expected, user.Quota)
			require.Equal(t, expected, token.RemainQuota)
		})
	}
}

func TestUserModelDiscountAsyncTokensUseFrozenRateAndUpdateAccounting(t *testing.T) {
	truncate(t)
	seedUser(t, 1, 910)
	seedToken(t, 2, 1, "retry-billing-key", 910)
	seedChannel(t, 3)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 1).Update("used_quota", 90).Error)
	require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", 2).Update("used_quota", 90).Error)
	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", 3).Update("used_quota", 90).Error)
	task := makeTask(1, 3, 90, 2, BillingSourceWallet, 0)
	task.TaskID = "task_discount_tokens"
	task.PrivateData.BillingContext = &model.TaskBillingContext{OriginModelName: "frozen-discount-model", ModelRatio: 1, GroupRatio: .9, UserModelDiscount: .9}
	require.NoError(t, model.LOG_DB.Create(&model.Log{UserId: 1, ChannelId: 3, Type: model.LogTypeConsume, Quota: 90, AccountingStatus: "ok", AccountingUserFinalAmountUSD: 90 / common.QuotaPerUnit, Other: common.MapToJsonStr(map[string]any{"is_task": true, "task_id": task.TaskID})}).Error)
	RecalculateTaskQuotaByTokens(context.Background(), task, 200)
	require.Equal(t, 180, task.Quota)
	require.Equal(t, 820, getUserQuota(t, 1))
	require.Equal(t, 180, getUserUsedQuota(t, 1))
	var log model.Log
	require.NoError(t, model.LOG_DB.Where("type = ?", model.LogTypeConsume).Order("id ASC").First(&log).Error)
	require.InDelta(t, 180/common.QuotaPerUnit, log.AccountingUserFinalAmountUSD, 1e-12)
	var token model.Token
	require.NoError(t, model.DB.First(&token, 2).Error)
	require.Equal(t, 820, token.RemainQuota)
	require.Equal(t, 180, token.UsedQuota)
}

func TestUserModelDiscountAccountingDoesNotDiscountProcurement(t *testing.T) {
	truncate(t)
	require.NoError(t, model.DB.AutoMigrate(&model.ChannelModelPricing{}))
	seedUser(t, 1, 5000)
	one := 1.0
	recharge := 1.5
	markup := 2.0
	require.NoError(t, model.DB.Create(&model.Channel{Id: 915, Name: "discount-cost-audit", RechargeRate: &recharge, ApimasterPriceRatio: &markup}).Error)
	require.NoError(t, model.DB.Create(&model.ChannelModelPricing{ChannelId: 915, ModelName: "gpt-5", InputPrice: 2, OutputPrice: 8, GroupRatio: one}).Error)
	t.Cleanup(func() { model.DB.Where("channel_id = ?", 915).Delete(&model.ChannelModelPricing{}) })
	input := ConsumeAccountingInput{UserId: 1, ChannelId: 915, ModelName: "gpt-5", InputTokens: 1000, OutputTokens: 100, GroupRatio: .95, Quota: 3990}
	before := BuildConsumeAccountingFields(input)
	require.Greater(t, before.ChannelCostAmountUSD, 0.0)
	input.GroupRatio = .9
	input.UserModelDiscount = .9 / .95
	input.Quota = 3780
	after := BuildConsumeAccountingFields(input)
	require.Equal(t, before.ChannelCostAmountUSD, after.ChannelCostAmountUSD)
	require.InDelta(t, float64(input.Quota)/common.QuotaPerUnit, after.UserFinalAmountUSD, 1e-12)
	require.InDelta(t, before.UserPriceAmountUSD, after.UserPriceAmountUSD, 1e-12)
}

func TestUserModelDiscountSeedanceAsyncReconciliationAndRefund(t *testing.T) {
	truncate(t)
	seedUser(t, 1, 500000)
	seedToken(t, 2, 1, "retry-billing-key", 500000)
	seedChannel(t, 3)
	info := retryBillingInfo(500000)
	info.OriginModelName = "seedance-2.5"
	info.UserSetting.ModelDiscountRatios = map[string]float64{info.OriginModelName: .9 / .95}
	info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: 3}
	info.TaskRelayInfo = &relaycommon.TaskRelayInfo{PublicTaskID: "task_discount_seedance"}
	info.ForcePreConsume = true
	info.PriceData = types.PriceData{ModelPrice: .1, UsePrice: true, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: .95, GroupSpecialRatio: -1}, OtherRatios: map[string]float64{"seconds": 5, "size": 1}}
	info.PriceData.Quota = SeedanceSubmissionQuota(info.PriceData)
	ctx := retryBillingContext()
	ctx.Request = httptest.NewRequest("POST", "/v1/videos/generations", nil)
	require.Nil(t, PreConsumeBilling(ctx, info.PriceData.Quota, info))
	require.Equal(t, 225000, info.PriceData.Quota)
	require.NoError(t, SettleBilling(ctx, info, info.PriceData.Quota))
	LogTaskConsumption(ctx, info)
	task := makeTask(1, 3, info.PriceData.Quota, 2, BillingSourceWallet, 0)
	task.TaskID = info.PublicTaskID
	task.PrivateData.BillingContext = &model.TaskBillingContext{SeedanceTariff: true, OriginModelName: info.OriginModelName, ModelPrice: .1, GroupRatio: .9, UserModelDiscount: .9 / .95, OtherRatios: map[string]float64{"seconds": 5, "size": 1}}
	task.PrivateData.SeedanceRequest = map[string]any{"duration": 5}
	result := &relaycommon.TaskInfo{BillableSeconds: 4}
	actual := SeedanceTariffQuota(task, result)
	require.Equal(t, 180000, actual)
	require.True(t, RecalculateTaskQuota(context.Background(), task, actual, "test"))
	require.Equal(t, 320000, getUserQuota(t, 1))
	require.Equal(t, 180000, getUserUsedQuota(t, 1))
	var log model.Log
	require.NoError(t, model.LOG_DB.Where("type = ?", model.LogTypeConsume).Order("id ASC").First(&log).Error)
	require.InDelta(t, float64(actual)/common.QuotaPerUnit, log.AccountingUserFinalAmountUSD, 1e-12)
	refund := taskBillingOther(task)
	require.InDelta(t, .95, refund["group_ratio"], 1e-12)
	require.InDelta(t, .9/.95, refund["user_model_discount"], 1e-12)
	RefundTaskQuota(context.Background(), task, "failed")
	require.Equal(t, 500000, getUserQuota(t, 1))
	require.Zero(t, getUserUsedQuota(t, 1))
	var token model.Token
	var channel model.Channel
	require.NoError(t, model.DB.First(&token, 2).Error)
	require.NoError(t, model.DB.First(&channel, 3).Error)
	require.Equal(t, 500000, token.RemainQuota)
	require.Zero(t, channel.UsedQuota)
	require.NoError(t, model.LOG_DB.First(&log, log.Id).Error)
	require.Zero(t, log.AccountingUserFinalAmountUSD)
}

func TestUserModelDiscountRejectsInvalidValues(t *testing.T) {
	setting := dto.UserSetting{ModelDiscountRatios: map[string]float64{
		"too-high": 1.1,
		"zero":     0,
		"valid":    0.8,
	}}
	sanitized := SanitizeUserModelDiscountRatios(setting.ModelDiscountRatios)
	require.Equal(t, map[string]float64{"valid": 0.8}, sanitized)
	require.Equal(t, float64(1), ResolveUserModelDiscount(dto.UserSetting{ModelDiscountRatios: sanitized}, "missing"))
	require.Equal(t, 0.8, ResolveUserModelDiscount(dto.UserSetting{ModelDiscountRatios: sanitized}, "valid"))
}
