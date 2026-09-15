package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
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
