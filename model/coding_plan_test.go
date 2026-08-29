package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestParseCodingModelMultipliers(t *testing.T) {
	values, err := ParseCodingModelMultipliers(`{"deepseek-v4":0.400,"glm-5":0.333,"kimi-k3":1.000}`)
	require.NoError(t, err)
	require.Equal(t, 0.4, values["deepseek-v4"])
	require.Equal(t, 0.333, values["glm-5"])
	require.Equal(t, 1.0, values["kimi-k3"])

	for _, invalid := range []string{
		`{"glm":0.3333}`,
		`{"glm":0}`,
		`{"glm":-0.1}`,
		`{"glm":1.001}`,
		`{"":0.5}`,
	} {
		_, err := ParseCodingModelMultipliers(invalid)
		require.Error(t, err, invalid)
	}
}

func TestCodingPlanQuoteUsesRemainingAllowance(t *testing.T) {
	setupGPTSubscriptionTestDB(t)
	now := GetDBTimestamp()
	currentPlan := SubscriptionPlan{
		Id: 11001, Title: "Coding Pro", PlanType: SubscriptionPlanTypeCodingPlan,
		PriceAmount: 39, Currency: "USD", DurationUnit: SubscriptionDurationDay,
		DurationValue: 30, Enabled: true, TierLevel: 1,
		CodingOfficialAmountUSD: 79, TotalAmount: int64(79 * common.QuotaPerUnit),
		CodingModelMultipliers: `{"glm":0.350}`,
	}
	targetPlan := currentPlan
	targetPlan.Id = 11002
	targetPlan.Title = "Coding Max"
	targetPlan.TierLevel = 2
	targetPlan.PriceAmount = 60
	targetPlan.CodingOfficialAmountUSD = 120
	targetPlan.TotalAmount = int64(120 * common.QuotaPerUnit)
	require.NoError(t, DB.Create(&currentPlan).Error)
	require.NoError(t, DB.Create(&targetPlan).Error)
	InvalidateSubscriptionPlanCache(currentPlan.Id)
	InvalidateSubscriptionPlanCache(targetPlan.Id)
	sub := UserSubscription{
		Id: 11003, UserId: 11004, PlanId: currentPlan.Id, Status: "active",
		StartTime: now - 86400, EndTime: now + 29*86400,
		AmountTotal: currentPlan.TotalAmount, AmountUsed: currentPlan.TotalAmount / 2,
		PriceAmountSnapshot: 39, PaidAmountSnapshot: 39, TierLevelSnapshot: 1,
	}
	require.NoError(t, DB.Create(&sub).Error)

	kind, previousID, credit, payable, err := CalculateCodingPlanQuote(sub.UserId, &targetPlan)
	require.NoError(t, err)
	require.Equal(t, "upgrade", kind)
	require.Equal(t, sub.Id, previousID)
	require.InDelta(t, 19.5, credit, 0.000001)
	require.InDelta(t, 40.5, payable, 0.000001)
}

func TestCodingPlanQuoteMarksSamePlanAsRenewal(t *testing.T) {
	setupGPTSubscriptionTestDB(t)
	now := GetDBTimestamp()
	plan := SubscriptionPlan{
		Id: 11011, Title: "Coding Starter", PlanType: SubscriptionPlanTypeCodingPlan,
		PriceAmount: 5, Currency: "USD", DurationUnit: SubscriptionDurationDay,
		DurationValue: 30, Enabled: true, TierLevel: 1,
		CodingOfficialAmountUSD: 10, TotalAmount: int64(10 * common.QuotaPerUnit),
		CodingModelMultipliers: `{"glm":0.350}`,
	}
	require.NoError(t, DB.Create(&plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	sub := UserSubscription{
		Id: 11012, UserId: 11013, PlanId: plan.Id, Status: "active",
		StartTime: now - 86400, EndTime: now + 29*86400,
		AmountTotal: plan.TotalAmount, AmountUsed: plan.TotalAmount / 2,
		PriceAmountSnapshot: 5, PaidAmountSnapshot: 5, TierLevelSnapshot: 1,
	}
	require.NoError(t, DB.Create(&sub).Error)

	kind, previousID, credit, payable, err := CalculateCodingPlanQuote(sub.UserId, &plan)
	require.NoError(t, err)
	require.Equal(t, "renewal", kind)
	require.Equal(t, sub.Id, previousID)
	require.InDelta(t, 2.5, credit, 0.000001)
	require.InDelta(t, 2.5, payable, 0.000001)
}

func TestCompleteCodingRenewalStartsFreshCycleNow(t *testing.T) {
	setupGPTSubscriptionTestDB(t)
	now := GetDBTimestamp()
	plan := SubscriptionPlan{
		Id: 11101, Title: "Coding Pro", PlanType: SubscriptionPlanTypeCodingPlan,
		PriceAmount: 39, Currency: "USD", DurationUnit: SubscriptionDurationDay,
		DurationValue: 30, Enabled: true, TierLevel: 1,
		CodingOfficialAmountUSD: 79, TotalAmount: int64(79 * common.QuotaPerUnit),
		CodingModelMultipliers: `{"glm":0.350}`,
	}
	require.NoError(t, DB.Create(&plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	old := UserSubscription{
		Id: 11102, UserId: 11103, PlanId: plan.Id, Status: "active",
		StartTime: now - 5*86400, EndTime: now + 25*86400,
		AmountTotal: plan.TotalAmount, AmountUsed: plan.TotalAmount / 3,
		PaidAmountSnapshot: 39,
	}
	require.NoError(t, DB.Create(&old).Error)
	order := SubscriptionOrder{
		Id: 11104, UserId: old.UserId, PlanId: plan.Id, Money: 13, ListPrice: 39, CreditAmount: 26,
		OrderType: "renewal", PreviousSubscriptionId: old.Id,
	}
	created, err := completeCodingPlanOrderTx(DB, &order, &plan)
	require.NoError(t, err)
	require.EqualValues(t, 0, created.AmountUsed)
	require.Equal(t, plan.TotalAmount, created.AmountTotal)
	require.InDelta(t, 39, created.PaidAmountSnapshot, 0.000001)
	require.InDelta(t, 39, created.PriceAmountSnapshot, 0.000001)
	require.WithinDuration(t, time.Unix(now+30*86400, 0), time.Unix(created.EndTime, 0), 2*time.Second)

	var cancelled UserSubscription
	require.NoError(t, DB.First(&cancelled, old.Id).Error)
	require.Equal(t, "cancelled", cancelled.Status)
}

func TestCompleteCodingPlanNormalizesLegacyPurchaseRenewal(t *testing.T) {
	setupGPTSubscriptionTestDB(t)
	now := GetDBTimestamp()
	plan := SubscriptionPlan{
		Id: 11121, Title: "Coding Starter", PlanType: SubscriptionPlanTypeCodingPlan,
		PriceAmount: 5, Currency: "USD", DurationUnit: SubscriptionDurationDay,
		DurationValue: 30, Enabled: true, TierLevel: 1,
		CodingOfficialAmountUSD: 10, TotalAmount: int64(10 * common.QuotaPerUnit),
		CodingModelMultipliers: `{"glm":0.350}`,
	}
	require.NoError(t, DB.Create(&plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	old := UserSubscription{
		Id: 11122, UserId: 11123, PlanId: plan.Id, Status: "active",
		StartTime: now - 86400, EndTime: now + 29*86400,
		AmountTotal: plan.TotalAmount, AmountUsed: plan.TotalAmount / 2,
		PaidAmountSnapshot: 5,
	}
	require.NoError(t, DB.Create(&old).Error)
	order := SubscriptionOrder{
		Id: 11124, UserId: old.UserId, PlanId: plan.Id, Money: 2.5,
		ListPrice: 5, CreditAmount: 2.5, OrderType: "purchase",
		PreviousSubscriptionId: old.Id,
	}

	created, err := completeCodingPlanOrderTx(DB, &order, &plan)
	require.NoError(t, err)
	require.Equal(t, "renewal", order.OrderType)
	require.Equal(t, order.Id, created.CurrentCycleId)
	require.WithinDuration(t, time.Unix(now+30*86400, 0), time.Unix(created.EndTime, 0), 2*time.Second)

	var cancelled UserSubscription
	require.NoError(t, DB.First(&cancelled, old.Id).Error)
	require.Equal(t, "cancelled", cancelled.Status)
}

func TestExpireDueCodingPlansRecordsUnusedAllowanceOnce(t *testing.T) {
	setupGPTSubscriptionTestDB(t)
	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })

	now := GetDBTimestamp()
	expiredAt := now - 36*3600
	user := User{
		Id: 11111, Username: "coding-expiry", Password: "password",
		AffCode: "coding-expiry", Role: common.RoleCommonUser,
	}
	require.NoError(t, DB.Create(&user).Error)
	plan := SubscriptionPlan{
		Id: 11112, Title: "Coding Pro", PlanType: SubscriptionPlanTypeCodingPlan,
		PriceAmount: 39, Currency: "USD", DurationUnit: SubscriptionDurationDay,
		DurationValue: 30, Enabled: true, TierLevel: 1,
		CodingOfficialAmountUSD: 79, TotalAmount: int64(79 * common.QuotaPerUnit),
		CodingModelMultipliers: `{"glm":0.350}`,
	}
	require.NoError(t, DB.Create(&plan).Error)
	subscriptions := []UserSubscription{
		{
			Id: 11113, UserId: user.Id, PlanId: plan.Id, Status: "active",
			StartTime: expiredAt - 30*86400, EndTime: expiredAt,
			AmountTotal: plan.TotalAmount, AmountUsed: int64(59 * common.QuotaPerUnit),
			PriceAmountSnapshot: 39, PaidAmountSnapshot: 39,
		},
		{
			Id: 11114, UserId: user.Id, PlanId: plan.Id, Status: "active",
			StartTime: expiredAt - 30*86400, EndTime: expiredAt,
			AmountTotal: plan.TotalAmount, AmountUsed: plan.TotalAmount,
			PriceAmountSnapshot: 39, PaidAmountSnapshot: 39,
		},
		{
			Id: 11115, UserId: user.Id, PlanId: plan.Id, Status: "cancelled",
			StartTime: expiredAt - 30*86400, EndTime: expiredAt,
			AmountTotal: plan.TotalAmount, AmountUsed: int64(10 * common.QuotaPerUnit),
			PriceAmountSnapshot: 39, PaidAmountSnapshot: 39,
		},
	}
	require.NoError(t, DB.Create(&subscriptions).Error)

	expiredCount, err := ExpireDueSubscriptions(100)
	require.NoError(t, err)
	require.Equal(t, 2, expiredCount)

	var events []SubscriptionExpiryRevenue
	require.NoError(t, DB.Find(&events).Error)
	require.Len(t, events, 1)
	event := events[0]
	require.Equal(t, subscriptions[0].Id, event.SubscriptionId)
	require.Equal(t, expiredAt, event.ExpiredAt)
	require.EqualValues(t, 20*common.QuotaPerUnit, event.ExpiredAllowanceQuota)
	require.InDelta(t, 20, event.ExpiredAllowanceUSD, 1e-9)
	require.InDelta(t, 39, event.PaidValueBasisUSD, 1e-9)
	require.InDelta(t, 39.0*20.0/79.0, event.ExpiredToRevenueUSD, 1e-9)

	daily, err := GetBillingCodingPlanExpiryDaily(expiredAt-1, expiredAt+1)
	require.NoError(t, err)
	day := billingDayStartUnix(expiredAt)
	require.Len(t, daily, 1)
	require.Equal(t, int64(1), daily[day].ExpiredCount)
	require.InDelta(t, 20, daily[day].ExpiredAllowanceUSD, 1e-9)
	require.InDelta(t, 9.8734177215, daily[day].ExpiryRevenueUSD, 1e-9)

	expiredCount, err = ExpireDueSubscriptions(100)
	require.NoError(t, err)
	require.Zero(t, expiredCount)
	var eventCount int64
	require.NoError(t, DB.Model(&SubscriptionExpiryRevenue{}).Count(&eventCount).Error)
	require.Equal(t, int64(1), eventCount)
}

func TestCodingPlanPreConsumeChecksLiveModelMap(t *testing.T) {
	setupGPTSubscriptionTestDB(t)
	now := GetDBTimestamp()
	plan := SubscriptionPlan{
		Id: 11201, Title: "Coding", PlanType: SubscriptionPlanTypeCodingPlan,
		PriceAmount: 30, Currency: "USD", DurationUnit: SubscriptionDurationDay,
		DurationValue: 30, Enabled: true, TierLevel: 1,
		CodingOfficialAmountUSD: 50, TotalAmount: int64(50 * common.QuotaPerUnit),
		CodingModelMultipliers: `{"glm":0.350}`,
	}
	require.NoError(t, DB.Create(&plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	sub := UserSubscription{Id: 11202, UserId: 11203, PlanId: plan.Id, Status: "active", StartTime: now, EndTime: now + 30*86400, AmountTotal: plan.TotalAmount}
	require.NoError(t, DB.Create(&sub).Error)

	_, err := PreConsumeUserSubscriptionByPlanMatcher("coding-unsupported", sub.UserId, "kimi", 0, 100, IsCodingPlan)
	require.ErrorContains(t, err, "subscription quota insufficient")
	result, err := PreConsumeUserSubscriptionByPlanMatcher("coding-supported", sub.UserId, "glm", 0, 100, IsCodingPlan)
	require.NoError(t, err)
	require.EqualValues(t, 100, result.PreConsumed)
}

func TestReverseCodingRenewalRestoresPreviousSubscription(t *testing.T) {
	setupGPTSubscriptionTestDB(t)
	now := GetDBTimestamp()
	plan := SubscriptionPlan{
		Id: 11301, Title: "Coding Pro", PlanType: SubscriptionPlanTypeCodingPlan,
		PriceAmount: 39, Currency: "USD", DurationUnit: SubscriptionDurationDay,
		DurationValue: 30, Enabled: true, TierLevel: 1,
		CodingOfficialAmountUSD: 79, TotalAmount: int64(79 * common.QuotaPerUnit),
		CodingModelMultipliers: `{"glm":0.350}`,
	}
	require.NoError(t, DB.Create(&plan).Error)
	old := UserSubscription{
		Id: 11302, UserId: 11303, PlanId: plan.Id, Status: "cancelled",
		StartTime: now - 5*86400, EndTime: now,
		AmountTotal: plan.TotalAmount, AmountUsed: plan.TotalAmount / 2,
		CurrentCycleId: 11300,
	}
	newCycle := UserSubscription{
		Id: 11304, UserId: old.UserId, PlanId: plan.Id, Status: "active",
		StartTime: now, EndTime: now + 30*86400,
		DurationSecondsSnapshot: 30 * 86400,
		AmountTotal:             plan.TotalAmount, CurrentCycleId: 11305,
	}
	order := SubscriptionOrder{
		Id: 11305, UserId: old.UserId, PlanId: plan.Id, Money: 19.5,
		TradeNo: "coding-renew-refund", Status: common.TopUpStatusSuccess,
		OrderType: "renewal", ProductType: SubscriptionPlanTypeCodingPlan,
		PreviousSubscriptionId: old.Id, PreviousEndTime: now + 25*86400,
		PreviousCycleId: old.CurrentCycleId,
	}
	require.NoError(t, DB.Create(&old).Error)
	require.NoError(t, DB.Create(&newCycle).Error)
	require.NoError(t, DB.Create(&order).Error)

	require.NoError(t, ReverseSubscriptionOrder(order.TradeNo, order.Money, "refund", "full"))
	var restoredOld, cancelledNew UserSubscription
	require.NoError(t, DB.First(&restoredOld, old.Id).Error)
	require.NoError(t, DB.First(&cancelledNew, newCycle.Id).Error)
	require.Equal(t, "active", restoredOld.Status)
	require.Equal(t, order.PreviousEndTime, restoredOld.EndTime)
	require.Equal(t, order.PreviousCycleId, restoredOld.CurrentCycleId)
	require.Equal(t, "cancelled", cancelledNew.Status)
}

func TestReinstateCodingRenewalReactivatesFreshCycle(t *testing.T) {
	setupGPTSubscriptionTestDB(t)
	now := GetDBTimestamp()
	plan := SubscriptionPlan{
		Id: 11401, Title: "Coding Pro", PlanType: SubscriptionPlanTypeCodingPlan,
		PriceAmount: 39, Currency: "USD", DurationUnit: SubscriptionDurationDay,
		DurationValue: 30, Enabled: true, TierLevel: 1,
		CodingOfficialAmountUSD: 79, TotalAmount: int64(79 * common.QuotaPerUnit),
		CodingModelMultipliers: `{"glm":0.350}`,
	}
	require.NoError(t, DB.Create(&plan).Error)
	old := UserSubscription{
		Id: 11402, UserId: 11403, PlanId: plan.Id, Status: "cancelled",
		StartTime: now - 5*86400, EndTime: now,
		AmountTotal: plan.TotalAmount, AmountUsed: plan.TotalAmount / 2,
		CurrentCycleId: 11400,
	}
	newCycle := UserSubscription{
		Id: 11404, UserId: old.UserId, PlanId: plan.Id, Status: "active",
		StartTime: now, EndTime: now + 30*86400,
		DurationSecondsSnapshot: 30 * 86400,
		AmountTotal:             plan.TotalAmount, CurrentCycleId: 11405,
	}
	order := SubscriptionOrder{
		Id: 11405, UserId: old.UserId, PlanId: plan.Id, Money: 19.5,
		TradeNo: "coding-renew-chargeback", Status: common.TopUpStatusSuccess,
		OrderType: "renewal", ProductType: SubscriptionPlanTypeCodingPlan,
		PreviousSubscriptionId: old.Id, PreviousEndTime: now + 25*86400,
		PreviousCycleId: old.CurrentCycleId, CompleteTime: now,
	}
	require.NoError(t, DB.Create(&old).Error)
	require.NoError(t, DB.Create(&newCycle).Error)
	require.NoError(t, DB.Create(&order).Error)

	require.NoError(t, ReverseSubscriptionOrder(order.TradeNo, order.Money, "chargeback", "lost"))
	require.NoError(t, ReinstateSubscriptionOrder(order.TradeNo, order.Money, "won"))

	var cancelledOld, restoredNew UserSubscription
	require.NoError(t, DB.First(&cancelledOld, old.Id).Error)
	require.NoError(t, DB.First(&restoredNew, newCycle.Id).Error)
	require.Equal(t, "cancelled", cancelledOld.Status)
	require.Equal(t, newCycle.StartTime, cancelledOld.EndTime)
	require.Equal(t, "active", restoredNew.Status)
	require.Equal(t, newCycle.StartTime+30*86400, restoredNew.EndTime)
	require.Equal(t, order.Id, restoredNew.CurrentCycleId)

	var reinstated SubscriptionOrder
	require.NoError(t, DB.First(&reinstated, order.Id).Error)
	require.Equal(t, common.TopUpStatusSuccess, reinstated.Status)
	require.Zero(t, reinstated.ChargebackAmount)
}
