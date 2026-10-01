package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

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
