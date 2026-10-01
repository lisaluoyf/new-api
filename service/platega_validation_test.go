package service

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func plategaValidationOrder(t *testing.T) *model.PlategaOrder {
	t.Helper()
	t.Setenv("PLATEGA_MERCHANT_ID", "test-merchant")
	t.Setenv("PLATEGA_X_SECRET", "test-secret")
	return &model.PlategaOrder{TradeNo: "test-order", Payload: "test-order", RubAmount: 100, PlategaTransactionId: "test-transaction", CreateRequestJSON: `{"payload":"test-order","paymentDetails":{"amount":100,"currency":"RUB"}}`, CreateResponseJSON: `{"id":"test-transaction","merchantId":"test-merchant","paymentDetails":"108.50 RUB"}`}
}
func TestPlategaAuthenticationFailsClosed(t *testing.T) {
	plategaValidationOrder(t)
	for _, tc := range []struct {
		name, merchant, secret string
		valid                  bool
	}{
		{"missing-both", "", "", false}, {"missing-secret", "test-merchant", "", false}, {"wrong-merchant", "other", "test-secret", false}, {"wrong-secret", "test-merchant", "other", false}, {"valid", "test-merchant", "test-secret", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.merchant != "" {
				h.Set("X-MerchantId", tc.merchant)
			}
			if tc.secret != "" {
				h.Set("X-Secret", tc.secret)
			}
			if tc.valid {
				require.NoError(t, AuthenticatePlategaCallback(h))
			} else {
				require.Error(t, AuthenticatePlategaCallback(h))
			}
		})
	}
	h := http.Header{}
	h.Add("X-MerchantId", "test-merchant")
	h.Add("X-Secret", "test-secret")
	h.Add("X-Secret", "test-secret")
	require.Error(t, AuthenticatePlategaCallback(h))
}
func TestPlategaCallbackStrictIdentityMoneyAndCurrency(t *testing.T) {
	o := plategaValidationOrder(t)
	base := PlategaCallbackPayload{ID: "test-transaction", Payload: "test-order", Amount: json.RawMessage(`108.50`), Currency: "RUB", PaymentMethod: json.RawMessage(`2`)}
	require.NoError(t, ValidatePlategaCallbackOrder(&base, o))
	legacy := base
	legacy.ID = ""
	legacy.TransactionId = "test-transaction"
	require.NoError(t, ValidatePlategaCallbackOrder(&legacy, o))
	for _, raw := range []string{"", "null", "0", "-1", `"bad"`, `"NaN"`, "108.501", "100", "108.51"} {
		t.Run("amount_"+raw, func(t *testing.T) {
			p := base
			p.Amount = json.RawMessage(raw)
			require.Error(t, ValidatePlategaCallbackOrder(&p, o))
		})
	}
	for _, mutate := range []func(*PlategaCallbackPayload){func(p *PlategaCallbackPayload) { p.Currency = "" }, func(p *PlategaCallbackPayload) { p.Currency = "USD" }, func(p *PlategaCallbackPayload) { p.TransactionId = "other-id" }, func(p *PlategaCallbackPayload) { p.ID = "" }, func(p *PlategaCallbackPayload) { p.Payload = "other-order" }, func(p *PlategaCallbackPayload) { p.PaymentMethod = nil }} {
		p := base
		mutate(&p)
		require.Error(t, ValidatePlategaCallbackOrder(&p, o))
	}
}
func TestPlategaAPIUsesFrozenInvoiceAndNestedPaymentDetails(t *testing.T) {
	o := plategaValidationOrder(t)
	r := PlategaTransactionStatusResponse{ID: "test-transaction", HistoricalMerchantID: "test-merchant", Payload: "test-order", PaymentDetails: PlategaPaymentDetails{Amount: 108.5, Currency: "RUB"}, PaymentMethod: "SBPQR", Status: "CONFIRMED"}
	require.NoError(t, ValidatePlategaAPIOrder(&r, o))
	for _, mutate := range []func(*PlategaTransactionStatusResponse){func(p *PlategaTransactionStatusResponse) { p.ID = "other" }, func(p *PlategaTransactionStatusResponse) { p.TransactionId = "other" }, func(p *PlategaTransactionStatusResponse) { p.MerchantID = "other" }, func(p *PlategaTransactionStatusResponse) { p.HistoricalMerchantID = ""; p.MerchantID = "" }, func(p *PlategaTransactionStatusResponse) { p.PaymentDetails.Amount = 0; p.Amount = 0 }, func(p *PlategaTransactionStatusResponse) { p.Currency = "USD" }, func(p *PlategaTransactionStatusResponse) { p.Payload = "other" }, func(p *PlategaTransactionStatusResponse) { p.PaymentDetails.Amount = 108.51; p.Amount = 108.51 }} {
		p := r
		mutate(&p)
		require.Error(t, ValidatePlategaAPIOrder(&p, o))
	}
}
