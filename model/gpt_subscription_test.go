package model

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupGPTSubscriptionTestDB(t *testing.T) {
	t.Helper()
	oldDB := DB
	oldSQLite := common.UsingSQLite
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&User{},
		&SubscriptionPlan{},
		&SubscriptionOrder{},
		&UserSubscription{},
		&SubscriptionPreConsumeRecord{},
		&TopUp{},
	))
	DB = db
	common.UsingSQLite = true
	t.Cleanup(func() {
		DB = oldDB
		common.UsingSQLite = oldSQLite
	})
}

func createGPTSubscriptionTestUser(t *testing.T, id int, quota int) {
	t.Helper()
	require.NoError(t, DB.Create(&User{
		Id: id, Username: fmt.Sprintf("gpt-subscription-%d", id),
		Status: common.UserStatusEnabled, Quota: quota,
	}).Error)
}

func TestActivateFreeGPTSubscription(t *testing.T) {
	setupGPTSubscriptionTestDB(t)
	const userID = 9001
	createGPTSubscriptionTestUser(t, userID, 1234)
	plan := SubscriptionPlan{
		Id: 9002, Title: "Free Pro", PlanType: SubscriptionPlanTypeGPTSubscription,
		PriceAmount: 0, Currency: "USD", DurationUnit: SubscriptionDurationDay,
		DurationValue: 30, Enabled: true, TierLevel: 1, MaxPurchasePerUser: 1,
		FiveHourAmount: 100, SevenDayAmount: 200,
	}
	require.NoError(t, DB.Create(&plan).Error)

	first, activated, err := ActivateFreeGPTSubscription(userID, plan.Id)
	require.NoError(t, err)
	require.True(t, activated)
	require.NotNil(t, first)
	require.Equal(t, "free", first.Source)
	require.Equal(t, plan.Id, first.PlanId)
	require.Equal(t, 1234, func() int { var user User; require.NoError(t, DB.First(&user, userID).Error); return user.Quota }())
	firstEnd := first.EndTime

	second, activated, err := ActivateFreeGPTSubscription(userID, plan.Id)
	require.NoError(t, err)
	require.False(t, activated)
	require.Equal(t, first.Id, second.Id)
	require.Equal(t, firstEnd, second.EndTime)

	var orderCount, topUpCount, subscriptionCount int64
	require.NoError(t, DB.Model(&SubscriptionOrder{}).Where("user_id = ?", userID).Count(&orderCount).Error)
	require.NoError(t, DB.Model(&TopUp{}).Where("user_id = ?", userID).Count(&topUpCount).Error)
	require.NoError(t, DB.Model(&UserSubscription{}).Where("user_id = ?", userID).Count(&subscriptionCount).Error)
	require.EqualValues(t, 1, orderCount)
	require.Zero(t, topUpCount)
	require.EqualValues(t, 1, subscriptionCount)
	require.False(t, HasSuccessfulTopUp(userID))

	var order SubscriptionOrder
	require.NoError(t, DB.Where("user_id = ?", userID).First(&order).Error)
	require.Equal(t, common.TopUpStatusSuccess, order.Status)
	require.Equal(t, PaymentProviderFree, order.PaymentProvider)
	require.Equal(t, PaymentMethodFree, order.PaymentMethod)
	require.Zero(t, order.Money)
	require.Equal(t, order.Id, first.CurrentCycleId)
}

func TestActivateFreeGPTSubscriptionRejectsPaidPlan(t *testing.T) {
	setupGPTSubscriptionTestDB(t)
	const userID = 9011
	createGPTSubscriptionTestUser(t, userID, 500)
	plan := SubscriptionPlan{
		Id: 9012, Title: "Paid Pro", PlanType: SubscriptionPlanTypeGPTSubscription,
		PriceAmount: 5, Currency: "USD", DurationUnit: SubscriptionDurationDay,
		DurationValue: 30, Enabled: true, TierLevel: 1,
	}
	require.NoError(t, DB.Create(&plan).Error)

	created, activated, err := ActivateFreeGPTSubscription(userID, plan.Id)
	require.ErrorIs(t, err, ErrGPTSubscriptionPlanNotFree)
	require.Nil(t, created)
	require.False(t, activated)
	var orderCount, subscriptionCount int64
	require.NoError(t, DB.Model(&SubscriptionOrder{}).Count(&orderCount).Error)
	require.NoError(t, DB.Model(&UserSubscription{}).Count(&subscriptionCount).Error)
	require.Zero(t, orderCount)
	require.Zero(t, subscriptionCount)
}

func TestActivateFreeGPTSubscriptionRejectsDowngrade(t *testing.T) {
	setupGPTSubscriptionTestDB(t)
	const userID = 9021
	createGPTSubscriptionTestUser(t, userID, 500)
	freePlan := SubscriptionPlan{
		Id: 9022, Title: "Free Pro", PlanType: SubscriptionPlanTypeGPTSubscription,
		PriceAmount: 0, Currency: "USD", DurationUnit: SubscriptionDurationDay,
		DurationValue: 30, Enabled: true, TierLevel: 1,
	}
	paidPlan := SubscriptionPlan{
		Id: 9023, Title: "Max", PlanType: SubscriptionPlanTypeGPTSubscription,
		PriceAmount: 20, Currency: "USD", DurationUnit: SubscriptionDurationDay,
		DurationValue: 30, Enabled: true, TierLevel: 3,
	}
	require.NoError(t, DB.Create(&freePlan).Error)
	require.NoError(t, DB.Create(&paidPlan).Error)
	now := GetDBTimestamp()
	require.NoError(t, DB.Create(&UserSubscription{
		UserId: userID, PlanId: paidPlan.Id, Status: "active", TierLevelSnapshot: 3,
		StartTime: now, EndTime: now + 86400,
	}).Error)

	created, activated, err := ActivateFreeGPTSubscription(userID, freePlan.Id)
	require.ErrorContains(t, err, "only renew or upgrade")
	require.Nil(t, created)
	require.False(t, activated)
	var activeCount int64
	require.NoError(t, DB.Model(&UserSubscription{}).
		Where("user_id = ? AND status = ?", userID, "active").Count(&activeCount).Error)
	require.EqualValues(t, 1, activeCount)
}

func TestGetPaymentNotificationContextMarksGPTSubscription(t *testing.T) {
	setupGPTSubscriptionTestDB(t)
	const userID = 9031
	createGPTSubscriptionTestUser(t, userID, 500)
	plan := SubscriptionPlan{
		Id: 9032, Title: "Pro+", PlanType: SubscriptionPlanTypeGPTSubscription,
		PriceAmount: 10, Currency: "USD", DurationUnit: SubscriptionDurationDay,
		DurationValue: 30, Enabled: true, TierLevel: 2,
	}
	require.NoError(t, DB.Create(&plan).Error)
	order := SubscriptionOrder{
		UserId: userID, PlanId: plan.Id, Money: 10, TradeNo: "gpt-notice-epay",
		OrderType: "upgrade", PaymentMethod: "wxpay", PaymentProvider: PaymentProviderEpay,
		ProviderPayload: common.GetJsonString(map[string]any{
			"payment_snapshot": map[string]any{
				"charge_amount": "73.00", "charge_currency": "CNY",
			},
		}),
		Status: common.TopUpStatusSuccess,
	}
	require.NoError(t, DB.Create(&order).Error)
	require.NoError(t, DB.Create(&UserSubscription{
		UserId: userID, PlanId: plan.Id, CurrentCycleId: order.Id,
		PlanTitleSnapshot: "Pro+", Status: "active",
	}).Error)
	require.NoError(t, DB.Model(&plan).Update("title", "Renamed plan").Error)
	InvalidateSubscriptionPlanCache(plan.Id)

	context := getPaymentNotificationContext(userID, order.TradeNo)
	require.True(t, context.IsSubscription)
	require.True(t, context.IsGPTSubscription)
	require.Equal(t, "Pro+", context.PlanTitle)
	require.Equal(t, "升级", context.OrderType)
	require.Equal(t, "¥73.00", context.ActualPayment)
}

func TestGetPaymentNotificationContextMarksStandardSubscription(t *testing.T) {
	setupGPTSubscriptionTestDB(t)
	const userID = 9041
	createGPTSubscriptionTestUser(t, userID, 500)
	plan := SubscriptionPlan{
		Id: 9042, Title: "Standard", PlanType: SubscriptionPlanTypeStandard,
		PriceAmount: 20, Currency: "USD", DurationUnit: SubscriptionDurationDay,
		DurationValue: 30, Enabled: true,
	}
	require.NoError(t, DB.Create(&plan).Error)
	order := SubscriptionOrder{
		UserId: userID, PlanId: plan.Id, Money: 20, TradeNo: "standard-subscription-notice",
		OrderType: "purchase", PaymentMethod: PaymentMethodPayPal, PaymentProvider: PaymentProviderPayPal,
		Status: common.TopUpStatusSuccess,
	}
	require.NoError(t, DB.Create(&order).Error)
	InvalidateSubscriptionPlanCache(plan.Id)

	context := getPaymentNotificationContext(userID, order.TradeNo)
	require.True(t, context.IsSubscription)
	require.False(t, context.IsGPTSubscription)
	require.Equal(t, "Standard", context.PlanTitle)
	require.Equal(t, "新购", context.OrderType)
	require.Equal(t, "$20.00", context.ActualPayment)
}

func TestReverseRenewalRestoresPreviousEntitlement(t *testing.T) {
	setupGPTSubscriptionTestDB(t)
	now := GetDBTimestamp()
	order := SubscriptionOrder{
		Id: 9901, UserId: 9902, PlanId: 9903, Money: 10, TradeNo: "renew-refund",
		OrderType: "renewal", PreviousSubscriptionId: 9904,
		PreviousEndTime: now + 10*86400, PreviousCycleId: 8801,
		Status: common.TopUpStatusSuccess,
	}
	require.NoError(t, DB.Create(&order).Error)
	sub := UserSubscription{
		Id: 9904, UserId: order.UserId, PlanId: order.PlanId, Status: "active",
		StartTime: now - 86400, EndTime: order.PreviousEndTime + 30*86400,
		CurrentCycleId: order.Id,
	}
	require.NoError(t, DB.Create(&sub).Error)
	require.NoError(t, DB.Create(&TopUp{UserId: order.UserId, TradeNo: order.TradeNo, Status: common.TopUpStatusSuccess}).Error)

	require.NoError(t, ReverseSubscriptionOrder(order.TradeNo, 4, "refund", "partial"))
	var partial UserSubscription
	require.NoError(t, DB.First(&partial, sub.Id).Error)
	require.Equal(t, sub.EndTime, partial.EndTime)

	require.NoError(t, ReverseSubscriptionOrder(order.TradeNo, 10, "refund", "full"))
	var restored UserSubscription
	require.NoError(t, DB.First(&restored, sub.Id).Error)
	require.Equal(t, order.PreviousEndTime, restored.EndTime)
	require.Equal(t, order.PreviousCycleId, restored.CurrentCycleId)
	require.Equal(t, "active", restored.Status)
	var reversed SubscriptionOrder
	require.NoError(t, DB.First(&reversed, order.Id).Error)
	require.Equal(t, "refund", reversed.Status)
	require.EqualValues(t, 10, reversed.RefundAmount)
}

func TestReinstateRenewalAfterChargeback(t *testing.T) {
	setupGPTSubscriptionTestDB(t)
	now := GetDBTimestamp()
	order := SubscriptionOrder{
		Id: 9911, UserId: 9912, PlanId: 9913, Money: 10, TradeNo: "renew-chargeback",
		OrderType: "renewal", PreviousSubscriptionId: 9914,
		PreviousEndTime: now + 10*86400, PreviousCycleId: 8811,
		Status: common.TopUpStatusSuccess, CompleteTime: now,
	}
	require.NoError(t, DB.Create(&order).Error)
	sub := UserSubscription{
		Id: 9914, UserId: order.UserId, PlanId: order.PlanId, Status: "active",
		StartTime: now - 86400, EndTime: order.PreviousEndTime + 30*86400,
		DurationSecondsSnapshot: 30 * 86400, CurrentCycleId: order.Id,
	}
	require.NoError(t, DB.Create(&sub).Error)
	require.NoError(t, DB.Create(&TopUp{UserId: order.UserId, TradeNo: order.TradeNo, Status: common.TopUpStatusSuccess}).Error)

	require.NoError(t, ReverseSubscriptionOrder(order.TradeNo, 10, "chargeback", "lost"))
	require.NoError(t, ReinstateSubscriptionOrder(order.TradeNo, 10, "won"))
	require.NoError(t, ReinstateSubscriptionOrder(order.TradeNo, 10, "duplicate"))

	var restored UserSubscription
	require.NoError(t, DB.First(&restored, sub.Id).Error)
	require.Equal(t, order.PreviousEndTime+30*86400, restored.EndTime)
	require.Equal(t, order.Id, restored.CurrentCycleId)
	require.Equal(t, "active", restored.Status)
	var reinstated SubscriptionOrder
	require.NoError(t, DB.First(&reinstated, order.Id).Error)
	require.Equal(t, common.TopUpStatusSuccess, reinstated.Status)
	require.Zero(t, reinstated.ChargebackAmount)
}

func TestReinstatePurchaseAfterChargeback(t *testing.T) {
	setupGPTSubscriptionTestDB(t)
	now := GetDBTimestamp()
	plan := SubscriptionPlan{Id: 9915, Title: "Pro", PlanType: SubscriptionPlanTypeGPTSubscription, Enabled: true}
	require.NoError(t, DB.Create(&plan).Error)
	order := SubscriptionOrder{
		Id: 9916, UserId: 9917, PlanId: plan.Id, Money: 5, TradeNo: "purchase-chargeback",
		OrderType: "purchase", Status: common.TopUpStatusSuccess, CompleteTime: now,
	}
	require.NoError(t, DB.Create(&order).Error)
	sub := UserSubscription{
		Id: 9918, UserId: order.UserId, PlanId: plan.Id, Status: "active",
		StartTime: now, EndTime: now + 30*86400,
		DurationSecondsSnapshot: 30 * 86400, CurrentCycleId: order.Id,
	}
	require.NoError(t, DB.Create(&sub).Error)
	require.NoError(t, DB.Create(&TopUp{UserId: order.UserId, TradeNo: order.TradeNo, Status: common.TopUpStatusSuccess}).Error)

	require.NoError(t, ReverseSubscriptionOrder(order.TradeNo, 5, "chargeback", "lost"))
	require.NoError(t, ReinstateSubscriptionOrder(order.TradeNo, 5, "won"))

	var restored UserSubscription
	require.NoError(t, DB.First(&restored, sub.Id).Error)
	require.Equal(t, "active", restored.Status)
	require.Equal(t, now+30*86400, restored.EndTime)
}

func TestReinstateKeepsFullRefundReversed(t *testing.T) {
	setupGPTSubscriptionTestDB(t)
	order := SubscriptionOrder{
		Id: 9919, UserId: 9920, PlanId: 9921, Money: 10,
		TradeNo: "refund-and-chargeback", OrderType: "purchase",
		Status: "chargeback", RefundAmount: 10, ChargebackAmount: 10,
	}
	require.NoError(t, DB.Create(&order).Error)

	require.NoError(t, ReinstateSubscriptionOrder(order.TradeNo, 10, "won"))

	var retained SubscriptionOrder
	require.NoError(t, DB.First(&retained, order.Id).Error)
	require.Equal(t, "refunded", retained.Status)
	require.EqualValues(t, 10, retained.RefundAmount)
	require.Zero(t, retained.ChargebackAmount)
}

func TestReinstateUpgradeAfterChargeback(t *testing.T) {
	setupGPTSubscriptionTestDB(t)
	now := GetDBTimestamp()
	oldPlan := SubscriptionPlan{Id: 9921, Title: "Pro", PlanType: SubscriptionPlanTypeGPTSubscription, Enabled: true}
	newPlan := SubscriptionPlan{Id: 9922, Title: "Max", PlanType: SubscriptionPlanTypeGPTSubscription, Enabled: true}
	require.NoError(t, DB.Create(&oldPlan).Error)
	require.NoError(t, DB.Create(&newPlan).Error)
	oldSub := UserSubscription{
		Id: 9923, UserId: 9924, PlanId: oldPlan.Id, Status: "cancelled",
		StartTime: now - 5*86400, EndTime: now, CurrentCycleId: 8821,
	}
	require.NoError(t, DB.Create(&oldSub).Error)
	order := SubscriptionOrder{
		Id: 9925, UserId: oldSub.UserId, PlanId: newPlan.Id, Money: 15, TradeNo: "upgrade-chargeback",
		OrderType: "upgrade", PreviousSubscriptionId: oldSub.Id,
		PreviousEndTime: now + 25*86400, PreviousCycleId: oldSub.CurrentCycleId,
		Status: common.TopUpStatusSuccess, CompleteTime: now,
	}
	require.NoError(t, DB.Create(&order).Error)
	upgraded := UserSubscription{
		Id: 9926, UserId: order.UserId, PlanId: newPlan.Id, Status: "active",
		StartTime: now, EndTime: now + 30*86400,
		DurationSecondsSnapshot: 30 * 86400, CurrentCycleId: order.Id,
	}
	require.NoError(t, DB.Create(&upgraded).Error)
	require.NoError(t, DB.Create(&TopUp{UserId: order.UserId, TradeNo: order.TradeNo, Status: common.TopUpStatusSuccess}).Error)

	require.NoError(t, ReverseSubscriptionOrder(order.TradeNo, 15, "chargeback", "lost"))
	require.NoError(t, ReinstateSubscriptionOrder(order.TradeNo, 15, "won"))

	var restoredUpgrade UserSubscription
	require.NoError(t, DB.First(&restoredUpgrade, upgraded.Id).Error)
	require.Equal(t, "active", restoredUpgrade.Status)
	require.Equal(t, now+30*86400, restoredUpgrade.EndTime)
	var cancelledOld UserSubscription
	require.NoError(t, DB.First(&cancelledOld, oldSub.Id).Error)
	require.Equal(t, "cancelled", cancelledOld.Status)
	require.Equal(t, now, cancelledOld.EndTime)
}

func TestSeedDefaultGPTSubscriptionPlans(t *testing.T) {
	setupGPTSubscriptionTestDB(t)
	require.NoError(t, SeedDefaultGPTSubscriptionPlans())
	require.NoError(t, SeedDefaultGPTSubscriptionPlans())

	var plans []SubscriptionPlan
	require.NoError(t, DB.Where("plan_type = ?", SubscriptionPlanTypeGPTSubscription).
		Order("tier_level asc").Find(&plans).Error)
	require.Len(t, plans, 5)
	require.Equal(t, []string{"Pro", "Pro+", "Max", "Ultra", "Power"}, []string{
		plans[0].Title, plans[1].Title, plans[2].Title, plans[3].Title, plans[4].Title,
	})
	require.EqualValues(t, 5*common.QuotaPerUnit, plans[0].FiveHourAmount)
	require.EqualValues(t, 1320*common.QuotaPerUnit, plans[4].SevenDayAmount)
	require.True(t, plans[2].Recommended)
	for _, plan := range plans {
		require.Equal(t, DefaultGPTSubscriptionModelAllowlist, plan.ModelAllowlist)
		require.Empty(t, plan.CardDescription)
	}
}

func TestGPTSubscriptionRollingLimitSettleAndRefund(t *testing.T) {
	setupGPTSubscriptionTestDB(t)
	plan := SubscriptionPlan{
		Id: 9101, Title: "Pro", PlanType: SubscriptionPlanTypeGPTSubscription,
		Currency: "USD", DurationUnit: SubscriptionDurationDay, DurationValue: 30,
		Enabled: true, TierLevel: 1, FiveHourAmount: 100, SevenDayAmount: 200,
		ModelAllowlist: "gpt-5.4,gpt-5.5",
	}
	require.NoError(t, DB.Create(&plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	now := GetDBTimestamp()
	sub := UserSubscription{
		Id: 9201, UserId: 9301, PlanId: plan.Id, Status: "active",
		StartTime: now - 100, EndTime: now + 86400, FiveHourAmount: 100,
		SevenDayAmount: 200, CurrentCycleId: 9401,
	}
	require.NoError(t, DB.Create(&sub).Error)

	reserved, err := PreConsumeUserSubscriptionByPlanMatcher(
		"paid-rolling-1", sub.UserId, "gpt-5.4", 0, 60, IsGPTPaidSubscriptionPlan,
	)
	require.NoError(t, err)
	require.EqualValues(t, 60, reserved.FiveHourUsedAfter)
	require.Equal(t, 9401, reserved.SubscriptionCycleId)

	_, err = PreConsumeUserSubscriptionByPlanMatcher(
		"paid-rolling-2", sub.UserId, "gpt-5.4", 0, 50, IsGPTPaidSubscriptionPlan,
	)
	require.ErrorContains(t, err, "quota insufficient")

	require.NoError(t, PostConsumeSubscriptionRequestDelta("paid-rolling-1", sub.Id, 10))
	five, seven, err := getGPTSubscriptionRollingUsageTx(DB, sub.UserId, GetDBTimestamp())
	require.NoError(t, err)
	require.EqualValues(t, 70, five)
	require.EqualValues(t, 70, seven)

	require.NoError(t, RefundSubscriptionPreConsume("paid-rolling-1"))
	five, seven, err = getGPTSubscriptionRollingUsageTx(DB, sub.UserId, GetDBTimestamp())
	require.NoError(t, err)
	require.Zero(t, five)
	require.Zero(t, seven)
	var updated UserSubscription
	require.NoError(t, DB.First(&updated, sub.Id).Error)
	require.Zero(t, updated.AmountUsed)
}

func TestGPTSubscriptionUpgradeQuoteAndRenewal(t *testing.T) {
	setupGPTSubscriptionTestDB(t)
	now := GetDBTimestamp()
	currentPlan := SubscriptionPlan{Id: 9501, Title: "Pro+", PlanType: SubscriptionPlanTypeGPTSubscription, PriceAmount: 10, Currency: "USD", DurationUnit: SubscriptionDurationDay, DurationValue: 30, Enabled: true, TierLevel: 2}
	targetPlan := SubscriptionPlan{Id: 9502, Title: "Max", PlanType: SubscriptionPlanTypeGPTSubscription, PriceAmount: 20, Currency: "USD", DurationUnit: SubscriptionDurationDay, DurationValue: 30, Enabled: true, TierLevel: 3}
	require.NoError(t, DB.Create(&currentPlan).Error)
	require.NoError(t, DB.Create(&targetPlan).Error)
	InvalidateSubscriptionPlanCache(currentPlan.Id)
	InvalidateSubscriptionPlanCache(targetPlan.Id)
	sub := UserSubscription{
		Id: 9601, UserId: 9701, PlanId: currentPlan.Id, Status: "active",
		StartTime: now - 15*86400, EndTime: now + 15*86400,
		PriceAmountSnapshot: 10, DurationSecondsSnapshot: 30 * 86400,
		TierLevelSnapshot: 2, AmountUsed: 123,
	}
	require.NoError(t, DB.Create(&sub).Error)

	kind, previousID, credit, payable, err := CalculateGPTSubscriptionQuote(sub.UserId, &targetPlan)
	require.NoError(t, err)
	require.Equal(t, "upgrade", kind)
	require.Equal(t, sub.Id, previousID)
	require.InDelta(t, 5, credit, 0.01)
	require.InDelta(t, 15, payable, 0.01)

	oldEnd := sub.EndTime
	order := SubscriptionOrder{Id: 9801, UserId: sub.UserId, PlanId: currentPlan.Id, OrderType: "renewal", PreviousSubscriptionId: sub.Id}
	renewed, err := completeGPTSubscriptionOrderTx(DB, &order, &currentPlan)
	require.NoError(t, err)
	require.Equal(t, oldEnd+30*86400, renewed.EndTime)
	require.EqualValues(t, 123, renewed.AmountUsed)
	require.Equal(t, order.Id, renewed.CurrentCycleId)
	require.WithinDuration(t, time.Unix(oldEnd+30*86400, 0), time.Unix(renewed.EndTime, 0), time.Second)
}
