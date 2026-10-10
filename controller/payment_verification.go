package controller

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/stripe/stripe-go/v81"
	stripesession "github.com/stripe/stripe-go/v81/checkout/session"
)

// Price and provider are frozen on the order. A provider lookup must never
// choose a different customer's order or update a price to fit a callback.
func verifiedPaymentAmountMatches(provider string, expected, paid float64) bool {
	if expected <= 0 || math.IsNaN(expected) || math.IsInf(expected, 0) {
		return false
	}
	return service.PaymentChargeMatches(provider, expected, paid)
}

func validateVerifiedPaymentPrice(trade, provider, currency string, paid float64) error {
	if trade == "" || currency != "USD" || paid <= 0 || math.IsNaN(paid) || math.IsInf(paid, 0) {
		return errors.New("invalid verified payment identity, currency or amount")
	}
	if order := model.GetSubscriptionOrderByTradeNo(trade); order != nil {
		if provider == model.PaymentProviderStripe && order.PaymentProvider == provider && strings.TrimSpace(order.ProviderPayload) != "" {
			snapshot, err := readSubscriptionStripePaymentSnapshot(order.ProviderPayload)
			if err != nil || snapshot.ChargeCurrency != currency || math.Abs(float64(snapshot.ChargeAmount)/100-paid) > 0.000001 {
				return errors.New("subscription Stripe frozen charge mismatch")
			}
			return nil
		}
		if order.PaymentProvider != provider || !verifiedPaymentAmountMatches(provider, order.Money, paid) {
			return errors.New("subscription provider or amount mismatch")
		}
		return nil
	}
	top := model.GetTopUpByTradeNo(trade)
	if top == nil || top.PaymentProvider != provider || !verifiedPaymentAmountMatches(provider, top.Money, paid) {
		return errors.New("wallet provider or amount mismatch")
	}
	return nil
}

var queryCreemCheckout = service.QueryCreemCheckout

func verifyCreemPaidCheckout(ctx context.Context, event *CreemWebhookEvent) error {
	raw, err := queryCreemCheckout(ctx, event.Object.Id)
	if err != nil {
		return err
	}
	// Query can expand product/customer or return their IDs; only payment facts
	// are decoded here. Customer profile data cannot establish payment success.
	var checkout struct {
		ID        string `json:"id"`
		RequestID string `json:"request_id"`
		Status    string `json:"status"`
		Mode      string `json:"mode"`
		Order     struct {
			ID         string `json:"id"`
			Status     string `json:"status"`
			AmountPaid int    `json:"amount_paid"`
			Subtotal   int    `json:"sub_total"`
			Tax        int    `json:"tax_amount"`
			Currency   string `json:"currency"`
		} `json:"order"`
	}
	if err := common.Unmarshal(raw, &checkout); err != nil {
		return err
	}
	mode := "prod"
	if setting.CreemTestMode {
		mode = "test"
	}
	if checkout.ID != event.Object.Id || checkout.RequestID != event.Object.RequestId || checkout.Status != "completed" || checkout.Mode != mode || checkout.Order.ID != event.Object.Order.Id || checkout.Order.Status != "paid" || checkout.Order.AmountPaid <= 0 || checkout.Order.Tax < 0 || checkout.Order.AmountPaid-checkout.Order.Tax != checkout.Order.Subtotal {
		return errors.New("Creem official checkout identity, status or money mismatch")
	}
	return validateVerifiedPaymentPrice(checkout.RequestID, model.PaymentProviderCreem, checkout.Order.Currency, float64(checkout.Order.Subtotal)/100)
}

var queryEpayOrder = service.QueryEpayOrder

func verifyEpayPaidOrder(ctx context.Context, callback *epay.VerifyRes) error {
	if callback == nil {
		return errors.New("missing Epay callback")
	}
	official, err := queryEpayOrder(ctx, callback.ServiceTradeNo)
	if err != nil {
		return err
	}
	paid, err := strconv.ParseFloat(string(official.Money), 64)
	callbackPaid, parseErr := strconv.ParseFloat(callback.Money, 64)
	if err != nil || parseErr != nil || paid <= 0 || math.IsNaN(paid) || math.IsInf(paid, 0) || math.IsNaN(callbackPaid) || math.IsInf(callbackPaid, 0) || math.Abs(paid-callbackPaid) > 0.000001 || official.TradeNo != callback.TradeNo || official.Type != callback.Type {
		return errors.New("Epay verified transaction or money mismatch")
	}
	if order := model.GetSubscriptionOrderByTradeNo(callback.ServiceTradeNo); order != nil {
		if order.PaymentProvider != model.PaymentProviderEpay {
			return model.ErrPaymentMethodMismatch
		}
		_, err = verifySubscriptionEpayCallbackAmount(callback.ServiceTradeNo, string(official.Money))
		return err
	}
	top := model.GetTopUpByTradeNo(callback.ServiceTradeNo)
	if top == nil || top.PaymentProvider != model.PaymentProviderEpay || math.Abs(top.Money-paid) > 0.000001 {
		return errors.New("Epay wallet amount or provider mismatch")
	}
	return nil
}

var queryStripePaidSession = func(ctx context.Context, id string) (*stripe.CheckoutSession, error) {
	return stripesession.Get(id, &stripe.CheckoutSessionParams{Params: stripe.Params{Context: ctx}})
}

func verifyStripePaidSession(ctx context.Context, event stripe.Event) error {
	stripe.Key = setting.StripeApiSecret
	official, err := queryStripePaidSession(ctx, event.GetObjectValue("id"))
	if err != nil {
		return errors.New("Stripe official query failed")
	}
	if official == nil || official.ID != event.GetObjectValue("id") || official.ClientReferenceID != event.GetObjectValue("client_reference_id") || official.Status != stripe.CheckoutSessionStatusComplete || official.PaymentStatus != stripe.CheckoutSessionPaymentStatusPaid {
		return errors.New("Stripe official checkout is not verified paid")
	}
	callbackAmount, parseErr := strconv.ParseInt(event.GetObjectValue("amount_total"), 10, 64)
	if parseErr != nil || callbackAmount != official.AmountTotal || strings.ToUpper(event.GetObjectValue("currency")) != strings.ToUpper(string(official.Currency)) {
		return errors.New("Stripe callback conflicts with official money")
	}
	return validateVerifiedPaymentPrice(official.ClientReferenceID, model.PaymentProviderStripe, strings.ToUpper(string(official.Currency)), float64(official.AmountTotal)/100)
}
