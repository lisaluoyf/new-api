package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/setting"
	"github.com/stretchr/testify/require"
)

func TestVerifyNowPaymentsIPN(t *testing.T) {
	originalSecret := setting.NowPaymentsIPNSecret
	t.Cleanup(func() { setting.NowPaymentsIPNSecret = originalSecret })
	setting.NowPaymentsIPNSecret = "test-ipn-secret"
	payload := []byte(`{ "payment_status": "finished", "payment_id": "123" }`)
	canonical := []byte(`{"payment_id":"123","payment_status":"finished"}`)
	mac := hmac.New(sha512.New, []byte(setting.NowPaymentsIPNSecret))
	_, err := mac.Write(canonical)
	require.NoError(t, err)
	signature := hex.EncodeToString(mac.Sum(nil))
	require.True(t, VerifyNowPaymentsIPN(payload, signature))
	require.False(t, VerifyNowPaymentsIPN(payload, "invalid"))
	require.False(t, VerifyNowPaymentsIPN([]byte(`{}`), signature))
}

func TestGetNowPaymentsPaymentAcceptsNumericPaymentID(t *testing.T) {
	originalBaseURL := nowPaymentsAPIBaseURL
	originalAPIKey := setting.NowPaymentsAPIKey
	t.Cleanup(func() {
		nowPaymentsAPIBaseURL = originalBaseURL
		setting.NowPaymentsAPIKey = originalAPIKey
	})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, "/payment/12345", request.URL.Path)
		require.Equal(t, "test-key", request.Header.Get("x-api-key"))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"payment_id":12345,"payment_status":"waiting","order_id":"order-1"}`))
	}))
	defer server.Close()
	nowPaymentsAPIBaseURL = server.URL
	setting.NowPaymentsAPIKey = "test-key"
	payment, err := GetNowPaymentsPayment(context.Background(), "12345")
	require.NoError(t, err)
	require.Equal(t, "12345", string(payment.PaymentID))
}

func TestCreateNowPaymentsInvoiceUsesHostedCheckoutWithoutPayCurrency(t *testing.T) {
	originalBaseURL := nowPaymentsAPIBaseURL
	originalAPIKey := setting.NowPaymentsAPIKey
	t.Cleanup(func() {
		nowPaymentsAPIBaseURL = originalBaseURL
		setting.NowPaymentsAPIKey = originalAPIKey
	})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, http.MethodPost, request.Method)
		require.Equal(t, "/invoice", request.URL.Path)
		require.Equal(t, "test-key", request.Header.Get("x-api-key"))
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		require.NotContains(t, string(body), "pay_currency")
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":987,"order_id":"order-1","price_amount":"10.00","price_currency":"usd","invoice_url":"https://nowpayments.io/payment/?iid=987"}`))
	}))
	defer server.Close()
	nowPaymentsAPIBaseURL = server.URL
	setting.NowPaymentsAPIKey = "test-key"

	invoice, err := CreateNowPaymentsInvoice(context.Background(), &NowPaymentsCreateInvoiceRequest{
		PriceAmount: 10, PriceCurrency: "usd", OrderID: "order-1",
	})
	require.NoError(t, err)
	require.Equal(t, "987", string(invoice.ID))
	require.Equal(t, 10.0, float64(invoice.PriceAmount))
	require.Equal(t, "https://nowpayments.io/payment/?iid=987", invoice.InvoiceURL)
}

func TestNowPaymentsErrorIncludesSafeKeyMetadataAndResponse(t *testing.T) {
	originalBaseURL := nowPaymentsAPIBaseURL
	originalAPIKey := setting.NowPaymentsAPIKey
	t.Cleanup(func() {
		nowPaymentsAPIBaseURL = originalBaseURL
		setting.NowPaymentsAPIKey = originalAPIKey
	})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusForbidden)
		_, _ = writer.Write([]byte(`{"code":"TEST_ERROR","message":"invoice creation is unavailable"}`))
	}))
	defer server.Close()
	nowPaymentsAPIBaseURL = server.URL
	setting.NowPaymentsAPIKey = "secret-api-key-HEGE"

	_, err := CreateNowPaymentsInvoice(context.Background(), &NowPaymentsCreateInvoiceRequest{
		PriceAmount: 10, PriceCurrency: "usd", OrderID: "order-1",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "status=403")
	require.Contains(t, err.Error(), "key_len=19")
	require.Contains(t, err.Error(), "key_suffix=HEGE")
	require.Contains(t, err.Error(), "TEST_ERROR")
	require.NotContains(t, err.Error(), "secret-api-key")
}
