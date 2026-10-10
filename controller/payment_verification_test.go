package controller

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v81"
)

func TestPaymentVerificationChecksProviderMoneyAndCurrency(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionOrder{}))
	require.NoError(t, db.Create(&model.TopUp{TradeNo: "test-payment", UserId: 1, Money: 10, PaymentProvider: model.PaymentProviderStripe, Status: "pending"}).Error)
	require.NoError(t, validateVerifiedPaymentPrice("test-payment", "stripe", "USD", 10))
	for _, amount := range []float64{0, -1, 9, 10.01, math.NaN(), math.Inf(1)} {
		require.Error(t, validateVerifiedPaymentPrice("test-payment", "stripe", "USD", amount))
	}
	require.Error(t, validateVerifiedPaymentPrice("test-payment", "paypal", "USD", 10))
	require.Error(t, validateVerifiedPaymentPrice("test-payment", "stripe", "", 10))
	require.Error(t, validateVerifiedPaymentPrice("test-payment", "stripe", "RUB", 10))
}

func TestPayPalVerificationMatchesFrozenCentAmount(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionOrder{}))
	for _, quote := range []struct {
		trade string
		money float64
		paid  float64
	}{
		{"pro-upgrade", 10.066003086419753, 10.07},
		{"ultra-upgrade", 90.06690972222222, 90.07},
		{"round-down", 90.064, 90.06},
	} {
		require.NoError(t, db.Create(&model.SubscriptionOrder{TradeNo: quote.trade, Money: quote.money, PaymentProvider: "paypal", Status: "pending"}).Error)
		require.NoError(t, validateVerifiedPaymentPrice(quote.trade, "paypal", "USD", quote.paid))
		for _, paid := range []float64{quote.paid - .01, quote.paid + .01, quote.money, math.NaN(), math.Inf(1)} {
			require.Error(t, validateVerifiedPaymentPrice(quote.trade, "paypal", "USD", paid))
		}
		require.Error(t, validateVerifiedPaymentPrice(quote.trade, "stripe", "USD", quote.paid))
		require.Error(t, validateVerifiedPaymentPrice(quote.trade, "paypal", "EUR", quote.paid))
	}
	require.NoError(t, db.Create(&model.TopUp{TradeNo: "fractional-wallet", Money: 10.066003086419753, PaymentProvider: "paypal", Status: "pending"}).Error)
	require.NoError(t, validateVerifiedPaymentPrice("fractional-wallet", "paypal", "USD", 10.07))
	require.Error(t, validateVerifiedPaymentPrice("fractional-wallet", "paypal", "USD", 10.06))
	// Stripe also compares the charge in integer cents.
	require.True(t, verifiedPaymentAmountMatches("stripe", 10.066003086419753, 10.07))
}

func TestStripeCallbackRequiresVerifiedPaidSession(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionOrder{}))
	require.NoError(t, db.Create(&model.TopUp{TradeNo: "test-payment", Money: 10, PaymentProvider: "stripe", Status: "pending"}).Error)
	old := queryStripePaidSession
	t.Cleanup(func() { queryStripePaidSession = old })
	event := stripe.Event{Data: &stripe.EventData{Object: map[string]interface{}{"id": "test-session", "client_reference_id": "test-payment", "amount_total": "1000", "currency": "usd"}}}
	paid := &stripe.CheckoutSession{ID: "test-session", ClientReferenceID: "test-payment", Status: stripe.CheckoutSessionStatusComplete, PaymentStatus: stripe.CheckoutSessionPaymentStatusPaid, AmountTotal: 1000, Currency: stripe.CurrencyUSD}
	queryStripePaidSession = func(context.Context, string) (*stripe.CheckoutSession, error) { return paid, nil }
	require.NoError(t, verifyStripePaidSession(context.Background(), event))
	for _, mutate := range []func(*stripe.CheckoutSession){func(p *stripe.CheckoutSession) { p.ID = "other" }, func(p *stripe.CheckoutSession) { p.ClientReferenceID = "other" }, func(p *stripe.CheckoutSession) { p.PaymentStatus = stripe.CheckoutSessionPaymentStatusUnpaid }, func(p *stripe.CheckoutSession) { p.AmountTotal = 999 }, func(p *stripe.CheckoutSession) { p.Currency = stripe.CurrencyEUR }} {
		copy := *paid
		mutate(&copy)
		queryStripePaidSession = func(context.Context, string) (*stripe.CheckoutSession, error) { return &copy, nil }
		require.Error(t, verifyStripePaidSession(context.Background(), event))
	}
	queryStripePaidSession = func(context.Context, string) (*stripe.CheckoutSession, error) { return nil, errors.New("timeout") }
	require.Error(t, verifyStripePaidSession(context.Background(), event))
	require.Equal(t, "pending", model.GetTopUpByTradeNo("test-payment").Status)
}

func TestCreemAndEpayCallbacksRequireOfficialPayment(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionOrder{}))
	require.NoError(t, db.Create(&model.TopUp{TradeNo: "test-creem", Money: 10, PaymentProvider: "creem", Status: "pending"}).Error)
	require.NoError(t, db.Create(&model.TopUp{TradeNo: "test-epay", Money: 70, PaymentProvider: "epay", Status: "pending"}).Error)
	oldCreem, oldEpay, oldMode := queryCreemCheckout, queryEpayOrder, setting.CreemTestMode
	setting.CreemTestMode = false
	t.Cleanup(func() {
		queryCreemCheckout = oldCreem
		queryEpayOrder = oldEpay
		setting.CreemTestMode = oldMode
	})
	e := CreemWebhookEvent{}
	e.Object.Id = "test-checkout"
	e.Object.RequestId = "test-creem"
	e.Object.Order.Id = "test-provider-order"
	raw := `{"id":"test-checkout","request_id":"test-creem","mode":"prod","status":"completed","order":{"id":"test-provider-order","status":"paid","amount_paid":1000,"sub_total":1000,"tax_amount":0,"currency":"USD"}}`
	queryCreemCheckout = func(context.Context, string) (json.RawMessage, error) { return json.RawMessage(raw), nil }
	require.NoError(t, verifyCreemPaidCheckout(context.Background(), &e))
	raw = `{"id":"test-checkout","request_id":"test-creem","mode":"prod","status":"pending"}`
	require.Error(t, verifyCreemPaidCheckout(context.Background(), &e))
	callback := &epay.VerifyRes{ServiceTradeNo: "test-epay", TradeNo: "test-provider-order", Type: "alipay", Money: "70.00"}
	queryEpayOrder = func(context.Context, string) (*service.EpayVerifiedOrder, error) {
		return &service.EpayVerifiedOrder{MerchantOrder: "test-epay", TradeNo: "test-provider-order", Money: dto.StringValue("70.00"), Type: "alipay"}, nil
	}
	require.NoError(t, verifyEpayPaidOrder(context.Background(), callback))
	callback.Money = "NaN"
	require.Error(t, verifyEpayPaidOrder(context.Background(), callback))
	callback.Money = "70.00"
	queryEpayOrder = func(context.Context, string) (*service.EpayVerifiedOrder, error) { return nil, errors.New("timeout") }
	require.Error(t, verifyEpayPaidOrder(context.Background(), callback))
}
