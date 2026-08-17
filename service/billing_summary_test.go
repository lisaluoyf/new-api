package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedConsumeLog(t *testing.T, log *model.Log) {
	t.Helper()
	require.NoError(t, model.LOG_DB.Create(log).Error)
}

func TestBillingDayStartUsesBeijingBoundary(t *testing.T) {
	// 2026-07-12 14:00:00 Asia/Shanghai == 2026-07-12 06:00:00 UTC
	got := billingDayStart(1_783_836_000)
	// 2026-07-12 00:00:00 Asia/Shanghai == 2026-07-11 16:00:00 UTC
	const want int64 = 1_783_785_600
	if got != want {
		t.Fatalf("billingDayStart() = %d, want %d", got, want)
	}
}

func TestPlanBillingDailyHybridRange_HistoryOnly(t *testing.T) {
	nowUnix := int64(1_783_836_000) // 2026-07-12 14:00:00 Asia/Shanghai
	start := int64(1_783_612_800)   // 2026-07-10 00:00:00 Asia/Shanghai
	end := int64(1_783_785_599)     // 2026-07-11 23:59:59 Asia/Shanghai

	plan := planBillingDailyHybridRange(start, end, nowUnix)
	if !plan.useSummary || plan.useRaw {
		t.Fatalf("expected history-only summary plan, got %+v", plan)
	}
	if plan.summaryStart != start || plan.summaryEnd != end {
		t.Fatalf("unexpected summary range: %+v", plan)
	}
}

func TestPlanBillingDailyHybridRange_TodayOnly(t *testing.T) {
	nowUnix := int64(1_783_836_000) // 2026-07-12 14:00:00 Asia/Shanghai
	start := int64(1_783_785_600)   // 2026-07-12 00:00:00 Asia/Shanghai
	end := int64(1_783_871_999)     // 2026-07-12 23:59:59 Asia/Shanghai

	plan := planBillingDailyHybridRange(start, end, nowUnix)
	if plan.useSummary || !plan.useRaw {
		t.Fatalf("expected today-only raw plan, got %+v", plan)
	}
	if plan.rawStart != start || plan.rawEnd != end {
		t.Fatalf("unexpected raw range: %+v", plan)
	}
}

func TestPlanBillingDailyHybridRange_MixedHistoryAndToday(t *testing.T) {
	nowUnix := int64(1_783_836_000) // 2026-07-12 14:00:00 Asia/Shanghai
	start := int64(1_783_612_800)   // 2026-07-10 00:00:00 Asia/Shanghai

	plan := planBillingDailyHybridRange(start, 0, nowUnix)
	if !plan.useSummary || !plan.useRaw {
		t.Fatalf("expected hybrid plan, got %+v", plan)
	}
	if plan.summaryStart != start {
		t.Fatalf("unexpected summary start: %+v", plan)
	}
	if plan.summaryEnd != 1_783_785_599 {
		t.Fatalf("unexpected summary end: %+v", plan)
	}
	if plan.rawStart != 1_783_785_600 || plan.rawEnd != 0 {
		t.Fatalf("unexpected raw range: %+v", plan)
	}
}

func TestGetBillingDailyFromRawLogs_SplitsSubscriptionMetrics(t *testing.T) {
	truncate(t)

	const (
		userID    = 1001
		channelID = 2001
		modelName = "gpt-5"
		baseTs    = int64(1_783_800_000)
	)

	seedUser(t, userID, 0)
	seedChannel(t, channelID)

	seedConsumeLog(t, &model.Log{
		UserId:                         userID,
		Type:                           model.LogTypeConsume,
		CreatedAt:                      baseTs,
		ModelName:                      modelName,
		ChannelId:                      channelID,
		Quota:                          100,
		Other:                          common.MapToJsonStr(map[string]any{"billing_source": BillingSourceWallet}),
		AccountingChannelCostAmountUSD: 1.25,
		AccountingUserFinalAmountUSD:   3.5,
		AccountingStatus:               "ok",
	})
	seedConsumeLog(t, &model.Log{
		UserId:                         userID,
		Type:                           model.LogTypeConsume,
		CreatedAt:                      baseTs,
		ModelName:                      modelName,
		ChannelId:                      channelID,
		Quota:                          615500,
		Other:                          common.MapToJsonStr(map[string]any{"billing_source": BillingSourceSubscription, "subscription_type": model.SubscriptionPlanTypeGPTTrial}),
		AccountingChannelCostAmountUSD: 0.75,
		AccountingUserFinalAmountUSD:   2.25,
		AccountingStatus:               "ok",
	})
	seedConsumeLog(t, &model.Log{
		UserId:                         userID,
		Type:                           model.LogTypeConsume,
		CreatedAt:                      baseTs,
		ModelName:                      modelName,
		ChannelId:                      channelID,
		Quota:                          500000,
		Other:                          common.MapToJsonStr(map[string]any{"billing_source": BillingSourceSubscription, "subscription_type": model.SubscriptionPlanTypeGPTSubscription}),
		AccountingChannelCostAmountUSD: 0.4,
		AccountingUserFinalAmountUSD:   1.5,
		AccountingStatus:               "ok",
	})
	// Non-OK rows should not affect the billing totals.
	seedConsumeLog(t, &model.Log{
		UserId:                         userID,
		Type:                           model.LogTypeConsume,
		CreatedAt:                      baseTs,
		ModelName:                      modelName,
		ChannelId:                      channelID,
		Quota:                          80,
		Other:                          common.MapToJsonStr(map[string]any{"billing_source": BillingSourceSubscription}),
		AccountingChannelCostAmountUSD: 9.99,
		AccountingUserFinalAmountUSD:   9.99,
		AccountingStatus:               "partial",
	})

	rows, err := model.GetBillingDailyFromRawLogs(baseTs-10, baseTs+10, modelName, channelID, "", "", "")
	require.NoError(t, err)
	require.Len(t, rows, 1)

	row := rows[0]
	assert.InDelta(t, 2.4, row.CostUSD, 1e-9)
	assert.InDelta(t, 5.731, row.RevenueUSD, 1e-9)
	assert.InDelta(t, 0.75, row.ExperienceCostUSD, 1e-9)
	assert.InDelta(t, 1.231, row.ExperienceBillingUSD, 1e-9)
	assert.InDelta(t, 0.4, row.PaidSubscriptionCostUSD, 1e-9)
	assert.InDelta(t, 1.0, row.PaidSubscriptionRevenueUSD, 1e-9)
	assert.Equal(t, int64(3), row.AccountingOKRequestCount)
	assert.Equal(t, int64(4), row.AccountingTargetReqCount)
	assert.Equal(t, int64(1), row.WalletUserCount)
	assert.Equal(t, int64(1), row.ExperienceUserCount)
	assert.Equal(t, int64(1), row.PaidSubscriptionUserCount)
}

func TestGetBillingUserCountsTotal_DistinctAcrossWholeRange(t *testing.T) {
	truncate(t)

	const (
		channelID = 2003
		modelName = "gpt-5"
		dayOneTs  = int64(1_783_800_000)
		dayTwoTs  = dayOneTs + 86_400
	)

	users := []model.User{
		{Id: 1003, Username: "wallet-user", AffCode: "wallet-user", Status: common.UserStatusEnabled},
		{Id: 1004, Username: "experience-user", AffCode: "experience-user", Status: common.UserStatusEnabled},
		{Id: 1005, Username: "hybrid-user", AffCode: "hybrid-user", Status: common.UserStatusEnabled},
		{Id: 1006, Username: "paid-user", AffCode: "paid-user", Status: common.UserStatusEnabled},
	}
	require.NoError(t, model.DB.Create(&users).Error)
	seedChannel(t, channelID)

	seedConsumeLog(t, &model.Log{
		UserId:                         1003,
		Type:                           model.LogTypeConsume,
		CreatedAt:                      dayOneTs,
		ModelName:                      modelName,
		ChannelId:                      channelID,
		Quota:                          100,
		Other:                          common.MapToJsonStr(map[string]any{"billing_source": BillingSourceWallet}),
		AccountingChannelCostAmountUSD: 1.0,
		AccountingUserFinalAmountUSD:   2.0,
		AccountingStatus:               "ok",
	})
	seedConsumeLog(t, &model.Log{
		UserId:                         1003,
		Type:                           model.LogTypeConsume,
		CreatedAt:                      dayTwoTs,
		ModelName:                      modelName,
		ChannelId:                      channelID,
		Quota:                          120,
		Other:                          common.MapToJsonStr(map[string]any{"billing_source": BillingSourceWallet}),
		AccountingChannelCostAmountUSD: 1.2,
		AccountingUserFinalAmountUSD:   2.4,
		AccountingStatus:               "ok",
	})
	seedConsumeLog(t, &model.Log{
		UserId:                         1004,
		Type:                           model.LogTypeConsume,
		CreatedAt:                      dayOneTs,
		ModelName:                      modelName,
		ChannelId:                      channelID,
		Quota:                          615500,
		Other:                          common.MapToJsonStr(map[string]any{"billing_source": BillingSourceSubscription, "subscription_type": model.SubscriptionPlanTypeGPTTrial}),
		AccountingChannelCostAmountUSD: 0.7,
		AccountingUserFinalAmountUSD:   2.1,
		AccountingStatus:               "ok",
	})
	seedConsumeLog(t, &model.Log{
		UserId:                         1005,
		Type:                           model.LogTypeConsume,
		CreatedAt:                      dayTwoTs,
		ModelName:                      modelName,
		ChannelId:                      channelID,
		Quota:                          200,
		Other:                          common.MapToJsonStr(map[string]any{"billing_source": BillingSourceWallet}),
		AccountingChannelCostAmountUSD: 0.5,
		AccountingUserFinalAmountUSD:   1.0,
		AccountingStatus:               "ok",
	})
	seedConsumeLog(t, &model.Log{
		UserId:                         1005,
		Type:                           model.LogTypeConsume,
		CreatedAt:                      dayTwoTs,
		ModelName:                      modelName,
		ChannelId:                      channelID,
		Quota:                          615500,
		Other:                          common.MapToJsonStr(map[string]any{"billing_source": BillingSourceSubscription, "subscription_type": model.SubscriptionPlanTypeGPTTrial}),
		AccountingChannelCostAmountUSD: 0.6,
		AccountingUserFinalAmountUSD:   1.8,
		AccountingStatus:               "ok",
	})
	seedConsumeLog(t, &model.Log{
		UserId:                         1006,
		Type:                           model.LogTypeConsume,
		CreatedAt:                      dayTwoTs,
		ModelName:                      modelName,
		ChannelId:                      channelID,
		Quota:                          500000,
		Other:                          common.MapToJsonStr(map[string]any{"billing_source": BillingSourceSubscription, "subscription_type": model.SubscriptionPlanTypeGPTSubscription}),
		AccountingChannelCostAmountUSD: 0.8,
		AccountingUserFinalAmountUSD:   2.4,
		AccountingStatus:               "ok",
	})

	totals, err := model.GetBillingUserCountsTotal(dayOneTs-10, dayTwoTs+10, modelName, channelID, "", "", "")
	require.NoError(t, err)
	assert.Equal(t, int64(2), totals.WalletUserCount)
	assert.Equal(t, int64(2), totals.ExperienceUserCount)
	assert.Equal(t, int64(1), totals.PaidSubscriptionUserCount)
}

func TestRunBillingSummaryOnce_SplitsSubscriptionMetrics(t *testing.T) {
	truncate(t)

	const (
		userID    = 1002
		channelID = 2002
		modelName = "gpt-5"
		baseTs    = int64(1_783_800_000)
	)

	seedUser(t, userID, 0)
	seedChannel(t, channelID)

	seedConsumeLog(t, &model.Log{
		UserId:                         userID,
		Type:                           model.LogTypeConsume,
		CreatedAt:                      baseTs,
		ModelName:                      modelName,
		ChannelId:                      channelID,
		Quota:                          100,
		Other:                          common.MapToJsonStr(map[string]any{"billing_source": BillingSourceWallet}),
		AccountingChannelCostAmountUSD: 1.25,
		AccountingUserFinalAmountUSD:   3.5,
		AccountingStatus:               "ok",
	})
	seedConsumeLog(t, &model.Log{
		UserId:                         userID,
		Type:                           model.LogTypeConsume,
		CreatedAt:                      baseTs,
		ModelName:                      modelName,
		ChannelId:                      channelID,
		Quota:                          615500,
		Other:                          common.MapToJsonStr(map[string]any{"billing_source": BillingSourceSubscription, "subscription_type": model.SubscriptionPlanTypeGPTTrial}),
		AccountingChannelCostAmountUSD: 0.75,
		AccountingUserFinalAmountUSD:   2.25,
		AccountingStatus:               "ok",
	})
	seedConsumeLog(t, &model.Log{
		UserId:                         userID,
		Type:                           model.LogTypeConsume,
		CreatedAt:                      baseTs,
		ModelName:                      modelName,
		ChannelId:                      channelID,
		Quota:                          500000,
		Other:                          common.MapToJsonStr(map[string]any{"billing_source": BillingSourceSubscription, "subscription_type": model.SubscriptionPlanTypeGPTSubscription}),
		AccountingChannelCostAmountUSD: 0.4,
		AccountingUserFinalAmountUSD:   1.5,
		AccountingStatus:               "ok",
	})

	originalNow := billingSummaryNow
	billingSummaryNow = func() time.Time { return time.Unix(baseTs+3600, 0) }
	defer func() { billingSummaryNow = originalNow }()

	runBillingSummaryOnce()

	var row model.BillingHourlySummary
	err := model.LOG_DB.Where("hour_bucket = ? AND model_name = ? AND channel_id = ?", baseTs/3600*3600, modelName, channelID).First(&row).Error
	require.NoError(t, err)
	assert.InDelta(t, 2.4, row.CostUSD, 1e-9)
	assert.InDelta(t, 5.731, row.RevenueUSD, 1e-9)
	assert.InDelta(t, 1.15, row.SubscriptionCostUSD, 1e-9)
	assert.InDelta(t, 2.231, row.SubscriptionBillingUSD, 1e-9)
	assert.InDelta(t, 0.4, row.PaidSubscriptionCostUSD, 1e-9)
	assert.InDelta(t, 1.0, row.PaidSubscriptionRevenueUSD, 1e-9)
	assert.Equal(t, int64(3), row.RequestCount)
}
