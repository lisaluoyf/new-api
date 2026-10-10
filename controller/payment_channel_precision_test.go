package controller

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v81"
)

func TestStripeOfficialQueryUsesFrozenChargeForLegacyUpgrade(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionOrder{}))
	snapshot := subscriptionStripePaymentSnapshot{PayableUSD: 90.06690972222222, ChargeAmount: 9007, ChargeCurrency: "USD"}
	order := model.SubscriptionOrder{TradeNo: "legacy-stripe-upgrade", Money: snapshot.PayableUSD, PaymentProvider: "stripe", Status: "pending", ProviderPayload: common.GetJsonString(snapshot)}
	require.NoError(t, db.Create(&order).Error)
	old := queryStripePaidSession
	t.Cleanup(func() { queryStripePaidSession = old })
	event := stripe.Event{Data: &stripe.EventData{Object: map[string]interface{}{"id": "precision-session", "client_reference_id": order.TradeNo, "amount_total": "9007", "currency": "usd"}}}
	paid := &stripe.CheckoutSession{ID: "precision-session", ClientReferenceID: order.TradeNo, Status: stripe.CheckoutSessionStatusComplete, PaymentStatus: stripe.CheckoutSessionPaymentStatusPaid, AmountTotal: 9007, Currency: stripe.CurrencyUSD}
	queryStripePaidSession = func(context.Context, string) (*stripe.CheckoutSession, error) { return paid, nil }
	require.NoError(t, verifyStripePaidSession(context.Background(), event))
	for _, payload := range []string{common.GetJsonString(snapshot), buildSubscriptionStripeCompletionPayload(order.ProviderPayload, map[string]any{"amount_total": "9007"})} {
		require.NoError(t, db.Model(&order).Update("provider_payload", payload).Error)
		require.NoError(t, verifyStripePaidSession(context.Background(), event))
		require.Error(t, validateVerifiedPaymentPrice(order.TradeNo, "stripe", "USD", 90.06))
		item := classifyReconciliationOrder(reconciliationCandidate{trade: order.TradeNo, provider: "stripe", purpose: "subscription", currency: "USD", money: order.Money, status: "success", payload: payload}, reconciliationProof{id: "precision-session", status: "complete/paid", known: true, paid: true, currency: "USD", amount: "90.07"})
		require.Equal(t, "matched", item.Result)
		require.Equal(t, "90.07", item.LocalAmount)
	}
	require.NoError(t, db.Model(&order).Update("provider_payload", `{"charge_amount":1}`).Error)
	require.Error(t, verifyStripePaidSession(context.Background(), event))
}

func TestGatewayLegacyUpgradeVerificationAndReconciliation(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionOrder{}))
	for _, provider := range []string{"clink", "waffo_pancake"} {
		order := model.SubscriptionOrder{TradeNo: provider + "-fractional-upgrade", Money: 10.066003086419753, PaymentProvider: provider, Status: "pending"}
		require.NoError(t, db.Create(&order).Error)
		require.NoError(t, validateVerifiedPaymentPrice(order.TradeNo, provider, "USD", 10.07))
		require.Error(t, validateVerifiedPaymentPrice(order.TradeNo, provider, "USD", 10.06))
		status := "succeeded"
		if provider == "clink" {
			status = "success"
		}
		row := reconciliationCandidate{trade: order.TradeNo, provider: provider, purpose: "subscription", currency: "USD", money: order.Money, status: "success"}
		proof := reconciliationProof{id: "upstream", status: status, known: true, paid: true, currency: "USD", amount: "10.07"}
		item := classifyReconciliationOrder(row, proof)
		require.Equal(t, "matched", item.Result)
		require.Equal(t, "10.07", item.LocalAmount)
		proof.amount = "10.06"
		require.Equal(t, "amount_mismatch", classifyReconciliationOrder(row, proof).Problem)
	}
}

func TestStripeFractionalUpgradeCompletesOnceWithoutWalletCredit(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionOrder{}, &model.SubscriptionPlan{}, &model.UserSubscription{}, &model.PaymentQueryReference{}))
	user := model.User{Id: 99121, Username: "stripe-upgrade-test", Status: common.UserStatusEnabled, Quota: 123456}
	require.NoError(t, db.Create(&user).Error)
	now := time.Now().Unix()
	plan := model.SubscriptionPlan{Id: 99122, Title: "Ultra", PlanType: model.SubscriptionPlanTypeGPTSubscription, PriceAmount: 100, Currency: "USD", DurationUnit: model.SubscriptionDurationDay, DurationValue: 30, Enabled: true, TierLevel: 4, FiveHourAmount: 50000000, SevenDayAmount: 475000000}
	require.NoError(t, db.Create(&plan).Error)
	model.InvalidateSubscriptionPlanCache(plan.Id)
	t.Cleanup(func() { model.InvalidateSubscriptionPlanCache(plan.Id) })
	previous := model.UserSubscription{Id: 99123, UserId: user.Id, PlanId: 4, Status: "active", StartTime: now - 100, EndTime: now + 86400, CurrentCycleId: 201}
	require.NoError(t, db.Create(&previous).Error)
	order := model.SubscriptionOrder{UserId: user.Id, PlanId: plan.Id, Money: 90.06690972222222, TradeNo: "precision-upgrade", PaymentMethod: "stripe", PaymentProvider: "stripe", ProviderPayload: common.GetJsonString(subscriptionStripePaymentSnapshot{PayableUSD: 90.06690972222222, ChargeAmount: 9007, ChargeCurrency: "USD"}), Status: "pending", OrderType: "upgrade", PreviousSubscriptionId: previous.Id, PreviousEndTime: previous.EndTime, PreviousCycleId: previous.CurrentCycleId}
	require.NoError(t, db.Create(&order).Error)

	oldQuery := queryStripePaidSession
	t.Cleanup(func() { queryStripePaidSession = oldQuery })
	event := stripe.Event{Data: &stripe.EventData{Object: map[string]interface{}{"id": "precision-session", "client_reference_id": order.TradeNo, "amount_total": "9007", "currency": "usd"}}}
	queryStripePaidSession = func(context.Context, string) (*stripe.CheckoutSession, error) {
		return &stripe.CheckoutSession{ID: "precision-session", ClientReferenceID: order.TradeNo, Status: stripe.CheckoutSessionStatusComplete, PaymentStatus: stripe.CheckoutSessionPaymentStatusPaid, AmountTotal: 9007, Currency: stripe.CurrencyUSD}, nil
	}
	for i := 0; i < 2; i++ {
		require.NoError(t, verifyStripePaidSession(context.Background(), event))
		require.NoError(t, fulfillOrder(context.Background(), event, order.TradeNo, "test-customer", "127.0.0.1"))
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
	for i := 0; i < 2; i++ {
		require.NoError(t, model.ReverseSubscriptionOrder(order.TradeNo, 90.07, "refund", "verified-refund"))
	}
	require.NoError(t, db.First(&previous, previous.Id).Error)
	require.Equal(t, "active", previous.Status)
	require.NoError(t, db.First(&entitlements[0], entitlements[0].Id).Error)
	require.Equal(t, "cancelled", entitlements[0].Status)
	require.NoError(t, db.First(&user, user.Id).Error)
	require.Equal(t, 123456, user.Quota)

}
