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

func TestEpayStatementExcludesOnlyProvenRomaAPIReceipts(t *testing.T) {
	old := paymentVerificationClient
	oldPID, oldKey := operation_setting.EpayId, operation_setting.EpayKey
	t.Cleanup(func() {
		paymentVerificationClient = old
		operation_setting.EpayId, operation_setting.EpayKey = oldPID, oldKey
	})
	operation_setting.EpayId, operation_setting.EpayKey = "shared-merchant", "test-secret"
	t.Setenv("EPAY_QUERY_BASE_URL", "https://gateway.invalid")
	body := `{"pid":"shared-merchant","day":"2026-10-12","complete":true,"verified_via":"official_provider_statement","payments":[
		{"id":"roma-id","trade_no":"roma-order","amount":"21.00","currency":"CNY","status":"SUCCESS","site":"romaapi"},
		{"id":"apimaster-id","trade_no":"apimaster-order","amount":"70.00","currency":"CNY","status":"SUCCESS","site":"apimaster"},
		{"id":"legacy-id","trade_no":"missing-local-order","amount":"35.00","currency":"CNY","status":"SUCCESS"},
		{"id":"unknown-id","trade_no":"unattributed-order","amount":"7.00","currency":"CNY","status":"SUCCESS","site":"unknown"}
	]}`
	paymentVerificationClient = &http.Client{Transport: reconciliationRoundTripper(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "/api/order/statement", r.URL.Path)
		require.Equal(t, "Bearer test-secret", r.Header.Get("Authorization"))
		var payload map[string]string
		require.NoError(t, common.DecodeJson(r.Body, &payload))
		require.Equal(t, "shared-merchant", payload["pid"])
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	day := time.Date(2026, 10, 12, 0, 0, 0, 0, time.FixedZone("UTC+8", 28800))
	rows, err := listEpayStatement(context.Background(), day)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	require.Equal(t, "apimaster-order", rows[0].TradeNo)
	require.Equal(t, "missing-local-order", rows[1].TradeNo)
	require.Equal(t, "unattributed-order", rows[2].TradeNo)
	// Site attribution must not turn an incomplete bill into complete coverage.
	body = strings.Replace(body, `"complete":true`, `"complete":false`, 1)
	rows, err = listEpayStatement(context.Background(), day)
	require.Error(t, err)
	require.Len(t, rows, 3)
	// Even attributed receipts require the expected merchant identity.
	body = strings.Replace(body, `"pid":"shared-merchant"`, `"pid":"wrong-merchant"`, 1)
	_, err = listEpayStatement(context.Background(), day)
	require.Error(t, err)
}
