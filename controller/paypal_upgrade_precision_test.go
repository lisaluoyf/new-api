package controller

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestPayPalFractionalUpgradeCompletesOnceWithoutWalletCredit(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionOrder{}, &model.SubscriptionPlan{}, &model.UserSubscription{}, &model.PaymentQueryReference{}))
	user := model.User{Id: 99121, Username: "paypal-upgrade-test", Status: common.UserStatusEnabled, Quota: 123456}
	require.NoError(t, db.Create(&user).Error)
	now := time.Now().Unix()
	plan := model.SubscriptionPlan{Id: 99122, Title: "Ultra", PlanType: model.SubscriptionPlanTypeGPTSubscription, PriceAmount: 100, Currency: "USD", DurationUnit: model.SubscriptionDurationDay, DurationValue: 30, Enabled: true, TierLevel: 4, FiveHourAmount: 50000000, SevenDayAmount: 475000000}
	require.NoError(t, db.Create(&plan).Error)
	model.InvalidateSubscriptionPlanCache(plan.Id)
	t.Cleanup(func() { model.InvalidateSubscriptionPlanCache(plan.Id) })
	previous := model.UserSubscription{Id: 99123, UserId: user.Id, PlanId: 4, Status: "active", StartTime: now - 100, EndTime: now + 86400, CurrentCycleId: 201}
	require.NoError(t, db.Create(&previous).Error)
	order := model.SubscriptionOrder{UserId: user.Id, PlanId: plan.Id, Money: 90.06690972222222, TradeNo: "precision-upgrade", PaymentMethod: "paypal", PaymentProvider: "paypal", Status: "pending", OrderType: "upgrade", PreviousSubscriptionId: previous.Id, PreviousCycleId: previous.CurrentCycleId}
	require.NoError(t, db.Create(&order).Error)
	oldQuery := queryPayPalCapture
	t.Cleanup(func() { queryPayPalCapture = oldQuery })
	payload := json.RawMessage(`{"id":"precision-capture","custom_id":"precision-upgrade","status":"COMPLETED","amount":{"value":"90.07","currency_code":"USD"}}`)
	queryPayPalCapture = func(context.Context, string) (json.RawMessage, error) { return payload, nil }
	for i := 0; i < 2; i++ {
		require.NoError(t, handlePayPalCaptureCompleted(context.Background(), payload, "127.0.0.1"))
	}
	require.NoError(t, db.First(&order, order.Id).Error)
	require.Equal(t, "success", order.Status)
	require.NoError(t, db.First(&previous, previous.Id).Error)
	require.Equal(t, "cancelled", previous.Status)
	var entitlements []model.UserSubscription
	require.NoError(t, db.Where("user_id = ? AND status = ?", user.Id, "active").Find(&entitlements).Error)
	require.Len(t, entitlements, 1)
	require.Equal(t, plan.Id, entitlements[0].PlanId)
	require.Equal(t, order.Id, entitlements[0].CurrentCycleId)
	require.Equal(t, plan.FiveHourAmount, entitlements[0].FiveHourAmount)
	require.NoError(t, db.First(&user, user.Id).Error)
	require.Equal(t, 123456, user.Quota)
	top := model.GetTopUpByTradeNo(order.TradeNo)
	require.NotNil(t, top)
	require.Equal(t, "success", top.Status)
	require.Equal(t, "precision-capture", top.PayPalCaptureID)
}

func TestPayPalLegacyUpgradeReconciliationUsesChargedCents(t *testing.T) {
	row := reconciliationCandidate{trade: "legacy-ultra", provider: "paypal", currency: "USD", money: 90.06690972222222, status: "success"}
	proof := reconciliationProof{id: "capture", status: "COMPLETED", currency: "USD", amount: "90.07", paid: true, known: true}
	require.Equal(t, "matched", classifyReconciliationOrder(row, proof).Result)
	proof.amount = "90.06"
	require.Equal(t, "amount_mismatch", classifyReconciliationOrder(row, proof).Problem)
}

func TestTransferredPayPalPaymentReconcilesAsWallet(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionOrder{}, &model.PaymentQueryReference{}))
	now := time.Now().Unix()
	require.NoError(t, db.Create(&model.SubscriptionOrder{UserId: 1, TradeNo: "converted-wallet", Money: 10.07, Status: "success", OrderType: "wallet_transfer", PaymentProvider: "paypal", CompleteTime: now}).Error)
	require.NoError(t, db.Create(&model.TopUp{UserId: 1, TradeNo: "converted-wallet", Money: 10.07, CreditedAmount: 10.07, Status: "success", PaymentProvider: "paypal", CompleteTime: now}).Error)
	rows, err := loadReconciliationCandidates(now-1, now+1, "paypal")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "wallet", rows[0].purpose)
}
