package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionReversalStatusAcrossProviders(t *testing.T) {
	for _, provider := range []string{"paypal", "clink", "stripe", "platega", "admin"} {
		for _, action := range []string{"refund", "chargeback"} {
			t.Run(provider+"/"+action, func(t *testing.T) {
				setupGPTSubscriptionTestDB(t)
				createGPTSubscriptionTestUser(t, 1, 12345)
				now := GetDBTimestamp()
				order := SubscriptionOrder{UserId: 1, PlanId: 1, Money: 69, TradeNo: provider + action, Status: common.TopUpStatusSuccess, OrderType: "purchase", PaymentProvider: provider}
				require.NoError(t, DB.Create(&order).Error)
				sub := UserSubscription{UserId: 1, PlanId: 1, Status: "active", StartTime: now, EndTime: now + 86400, CurrentCycleId: order.Id}
				require.NoError(t, DB.Create(&sub).Error)
				topup := TopUp{UserId: 1, TradeNo: order.TradeNo, Money: 69, Status: common.TopUpStatusSuccess, PaymentProvider: provider}
				require.NoError(t, DB.Create(&topup).Error)
				require.NoError(t, ReverseSubscriptionOrder(order.TradeNo, 20, action, "partial"))
				require.NoError(t, DB.First(&order, order.Id).Error)
				require.Equal(t, common.TopUpStatusSuccess, order.Status)
				require.NoError(t, DB.First(&sub, sub.Id).Error)
				require.Equal(t, "active", sub.Status)
				require.NoError(t, ReverseSubscriptionOrder(order.TradeNo, 69, action, "full"))
				want := action
				if action == "refund" {
					want = common.TopUpStatusRefunded
				}
				require.NoError(t, DB.First(&order, order.Id).Error)
				require.Equal(t, want, order.Status)
				require.NoError(t, DB.First(&topup, topup.Id).Error)
				require.Equal(t, want, topup.Status)
				require.NoError(t, DB.First(&sub, sub.Id).Error)
				require.Equal(t, "cancelled", sub.Status)
				require.NoError(t, ReverseSubscriptionOrder(order.TradeNo, 69, action, "duplicate"))
				var user User
				require.NoError(t, DB.First(&user, 1).Error)
				require.Equal(t, 12345, user.Quota)
			})
		}
	}
}

func TestDuplicateRefundDoesNotRewindLaterRenewal(t *testing.T) {
	for _, status := range []string{common.TopUpStatusRefunded, "refund"} {
		t.Run(status, func(t *testing.T) {
			setupGPTSubscriptionTestDB(t)
			now := GetDBTimestamp()
			order := SubscriptionOrder{UserId: 1, PlanId: 1, Money: 69, RefundAmount: 69, TradeNo: "old-refund", Status: status, OrderType: "renewal", PreviousSubscriptionId: 1, PreviousEndTime: now + 86400, PreviousCycleId: 1, ProductType: SubscriptionPlanTypeCodingPlan}
			require.NoError(t, DB.Create(&order).Error)
			sub := UserSubscription{Id: 1, UserId: 1, PlanId: 1, Status: "active", EndTime: now + 30*86400, CurrentCycleId: 999}
			require.NoError(t, DB.Create(&sub).Error)
			topup := TopUp{UserId: 1, TradeNo: order.TradeNo, Status: status, CompleteTime: now - 3600}
			require.NoError(t, DB.Create(&topup).Error)
			require.NoError(t, ReverseSubscriptionOrder(order.TradeNo, 69, "refund", "duplicate"))
			var after UserSubscription
			require.NoError(t, DB.First(&after, sub.Id).Error)
			require.Equal(t, sub.EndTime, after.EndTime)
			require.Equal(t, 999, after.CurrentCycleId)
			require.Equal(t, "active", after.Status)
			require.NoError(t, DB.First(&topup, topup.Id).Error)
			require.Equal(t, common.TopUpStatusRefunded, topup.Status)
			require.Equal(t, now-3600, topup.CompleteTime)
		})
	}
}

func TestNormalizeLegacyRefundStatusesOnlyChangesStatus(t *testing.T) {
	setupGPTSubscriptionTestDB(t)
	createGPTSubscriptionTestUser(t, 1, 12345)
	order := SubscriptionOrder{UserId: 1, Money: 69, RefundAmount: 69, TradeNo: "legacy", Status: "refund", ProviderPayload: "evidence", CompleteTime: 123}
	topup := TopUp{UserId: 1, Money: 69, TradeNo: order.TradeNo, Status: "refund", CompleteTime: 456, CreditedAmount: 69}
	sub := UserSubscription{UserId: 1, Status: "cancelled", EndTime: 789}
	require.NoError(t, DB.Create(&order).Error)
	require.NoError(t, DB.Create(&topup).Error)
	require.NoError(t, DB.Create(&sub).Error)
	for _, status := range []string{"success", "pending", "refunded", "chargeback"} {
		require.NoError(t, DB.Create(&TopUp{TradeNo: status, Status: status}).Error)
	}
	require.NoError(t, normalizeLegacyRefundStatuses(DB))
	require.NoError(t, normalizeLegacyRefundStatuses(DB))
	expectedOrder, expectedTopup := order, topup
	expectedOrder.Status, expectedTopup.Status = common.TopUpStatusRefunded, common.TopUpStatusRefunded
	require.NoError(t, DB.First(&order, order.Id).Error)
	require.NoError(t, DB.First(&topup, topup.Id).Error)
	require.Equal(t, expectedOrder, order)
	require.Equal(t, expectedTopup, topup)
	var user User
	require.NoError(t, DB.First(&user, 1).Error)
	require.Equal(t, 12345, user.Quota)
	var after UserSubscription
	require.NoError(t, DB.First(&after, sub.Id).Error)
	require.Equal(t, sub, after)
	for _, status := range []string{"success", "pending", "refunded", "chargeback"} {
		var other TopUp
		require.NoError(t, DB.Where("trade_no = ?", status).First(&other).Error)
		require.Equal(t, status, other.Status)
	}
}
