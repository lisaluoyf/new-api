package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

type reconciliationRoundTripper func(*http.Request) (*http.Response, error)

func (f reconciliationRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestEpayCustomQueryUsesSecretHeaderAndVerifiedOfficialFacts(t *testing.T) {
	oldClient := paymentVerificationClient
	oldPID, oldKey, oldAddress := operation_setting.EpayId, operation_setting.EpayKey, operation_setting.PayAddress
	t.Cleanup(func() {
		paymentVerificationClient = oldClient
		operation_setting.EpayId, operation_setting.EpayKey, operation_setting.PayAddress = oldPID, oldKey, oldAddress
	})
	operation_setting.EpayId = "anonymous-merchant"
	operation_setting.EpayKey = "anonymous-secret"
	operation_setting.PayAddress = "https://browser-proxy.invalid"
	t.Setenv("EPAY_QUERY_BASE_URL", "https://gateway.invalid")
	response := `{"code":1,"pid":"anonymous-merchant","out_trade_no":"anonymous-order","trade_no":"anonymous-provider-id","money":"1.00","currency":"CNY","type":"wxpay","status":1,"provider_status":"SUCCESS","verified_via":"official_provider_api"}`
	paymentVerificationClient = &http.Client{Transport: reconciliationRoundTripper(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "/api/order/query", r.URL.Path)
		require.Empty(t, r.URL.RawQuery)
		require.Equal(t, "Bearer anonymous-secret", r.Header.Get("Authorization"))
		var payload map[string]string
		require.NoError(t, common.DecodeJson(r.Body, &payload))
		require.Equal(t, "anonymous-merchant", payload["pid"])
		require.Equal(t, "anonymous-order", payload["out_trade_no"])
		require.NotContains(t, payload, "key")
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(response)), Header: make(http.Header)}, nil
	})}
	p, e := QueryEpayOrder(context.Background(), "anonymous-order")
	require.NoError(t, e)
	require.Equal(t, "SUCCESS", p.ProviderStatus)
	response = strings.Replace(response, `"status":1`, `"status":0`, 1)
	_, e = QueryEpayOrderState(context.Background(), "anonymous-order")
	require.NoError(t, e)
	_, e = QueryEpayOrder(context.Background(), "anonymous-order")
	require.Error(t, e)
	response = strings.Replace(response, `"currency":"CNY"`, `"currency":"USD"`, 1)
	_, e = QueryEpayOrderState(context.Background(), "anonymous-order")
	require.Error(t, e)
}

func TestPlategaStatementDiscoversIndependentSuccessfulOrders(t *testing.T) {
	t.Setenv("PLATEGA_MERCHANT_ID", "anonymous-merchant")
	t.Setenv("PLATEGA_X_SECRET", "anonymous-secret")
	old := paymentVerificationClient
	t.Cleanup(func() { paymentVerificationClient = old })
	paymentVerificationClient = &http.Client{Transport: reconciliationRoundTripper(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "/transaction/export/json", r.URL.Path)
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "anonymous-merchant", r.Header.Get("X-MerchantId"))
		require.Empty(t, r.URL.RawQuery)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`[{"recordId":"missing-id","createdAt":"2026-09-30 01:00:00","payload":"missing-local-order","status":"CONFIRMED","amount":108.5,"currencyCode":"RUB"},{"recordId":"cancelled-id","status":"CANCELED","amount":10,"currencyCode":"RUB"}]`))}, nil
	})}
	day := time.Date(2026, 9, 30, 0, 0, 0, 0, time.FixedZone("UTC+8", 28800))
	rows, e := ListReconciliationPayments(context.Background(), "platega", day, day.AddDate(0, 0, 1))
	require.NoError(t, e)
	require.Len(t, rows, 1)
	require.Equal(t, "missing-local-order", rows[0].TradeNo)
}

func TestStatementMerchantReferenceDoesNotRequireLocalOrder(t *testing.T) {
	p := waffoRefundPayment{OrderMerchantExternalID: "anonymous-missing-order"}
	trade, e := resolveWaffoMerchantReference(context.Background(), p)
	require.NoError(t, e)
	require.Equal(t, "anonymous-missing-order", trade)
}

func TestClinkStableOrderQueryPreservesSessionIdentity(t *testing.T) {
	t.Setenv("CLINK_SECRET_KEY", "test-secret")
	old := paymentVerificationClient
	t.Cleanup(func() { paymentVerificationClient = old })
	paymentVerificationClient = &http.Client{Transport: reconciliationRoundTripper(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/api/order/order_test", r.URL.Path)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":200,"data":{"orderId":"order_test","sessionId":"sess_expired","merchantReferenceId":"CLINK-test","status":"success","amountSubtotal":1,"amountTotal":117.83,"originalCurrency":"USD","paymentCurrency":"INR"}}`)), Header: make(http.Header)}, nil
	})}
	order, err := GetClinkOrder(context.Background(), "order_test")
	require.NoError(t, err)
	require.Equal(t, "sess_expired", order.SessionID)
	require.Equal(t, float64(1), order.AmountSubtotal)
	require.Equal(t, "USD", order.OriginalCurrency)
}
