package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestTransferredSubscriptionPaymentRemainsWalletOnReplayAndReversal(t *testing.T) {
	setupGPTSubscriptionTestDB(t)
	const userID = 99451
	const walletQuota = 5035000
	createGPTSubscriptionTestUser(t, userID, walletQuota+100)
	plan := SubscriptionPlan{Id: 99452, Title: "Pro", PlanType: SubscriptionPlanTypeGPTSubscription, Enabled: true}
	require.NoError(t, DB.Create(&plan).Error)
	order := SubscriptionOrder{UserId: userID, PlanId: plan.Id, TradeNo: "converted-paypal", OrderType: "wallet_transfer", Money: 10.07, Status: "success", PaymentProvider: "paypal", PaymentMethod: "paypal"}
	require.NoError(t, DB.Create(&order).Error)
	top := TopUp{UserId: userID, TradeNo: order.TradeNo, Money: 10.07, CreditedAmount: 10.07, Status: "success", PaymentProvider: "paypal", PaymentMethod: "paypal"}
	require.NoError(t, DB.Create(&top).Error)
	require.NoError(t, CompleteSubscriptionOrder(order.TradeNo, "replayed", "paypal", "paypal"))
	var count int64
	require.NoError(t, DB.Model(&UserSubscription{}).Where("user_id = ?", userID).Count(&count).Error)
	require.Zero(t, count)
	EnrichTopupsWithTransactionInfo([]*TopUp{&top})
	require.Equal(t, TopupTransactionTypeWallet, top.TransactionType)
	require.False(t, getPaymentNotificationContext(userID, order.TradeNo).IsSubscription)
	walletRows := applyTopupTransactionTypeFilter(DB.Model(&TopUp{}), TopupTransactionTypeWallet)
	require.NoError(t, walletRows.Count(&count).Error)
	require.EqualValues(t, 1, count)
	for i := 0; i < 2; i++ {
		require.NoError(t, ReverseSubscriptionOrder(order.TradeNo, 10.07, "refund", "verified-refund"))
	}
	var user User
	require.NoError(t, DB.First(&user, userID).Error)
	require.Equal(t, 100, user.Quota)
	require.NoError(t, DB.First(&top, top.Id).Error)
	require.Equal(t, common.TopUpStatusRefunded, top.Status)
	require.Equal(t, walletQuota, top.RefundedQuota)
	verified, err := VerifyPayPalRefundRecovery(&top)
	require.NoError(t, err)
	require.True(t, verified)
}
