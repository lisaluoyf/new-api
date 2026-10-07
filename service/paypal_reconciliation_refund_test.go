package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPayPalFullRefundRequiresIndependentAmountIdentityAndCompletedStatus(t *testing.T) {
	oldClient, oldToken, oldExpiry := paymentVerificationClient, payPalAccessToken, payPalTokenExpiry
	t.Cleanup(func() {
		paymentVerificationClient = oldClient
		payPalAccessToken = oldToken
		payPalTokenExpiry = oldExpiry
	})
	payPalAccessToken = "anonymous-token"
	payPalTokenExpiry = time.Now().Add(time.Hour)
	report := `{"total_pages":1,"transaction_details":[{"transaction_info":{"transaction_id":"refund-id","paypal_reference_id":"capture-id","paypal_reference_id_type":"TXN","transaction_event_code":"T1107","transaction_status":"S","transaction_amount":{"value":"-100.00","currency_code":"USD"}}}]}`
	refund := `{"id":"refund-id","status":"COMPLETED","amount":{"value":"100.00","currency_code":"USD"},"links":[{"rel":"up","href":"https://api.paypal.com/v2/payments/captures/capture-id"}]}`
	for _, tt := range []struct {
		name, report, refund string
		ok                   bool
	}{
		{"full", report, refund, true},
		{"IPR reversal", strings.Replace(report, "T1107", "T1116", 1), refund, true},
		{"IPR partial reversal", strings.Replace(strings.Replace(report, "T1107", "T1116", 1), "-100.00", "-20.00", 1), refund, false},
		{"hold release is not a refund", strings.Replace(report, "T1107", "T2106", 1), refund, false},
		{"unverified chargeback", strings.Replace(report, "T1107", "T1201", 1), refund, false},
		{"partial", strings.Replace(report, "-100.00", "-50.00", 1), refund, false},
		{"currency", report, strings.Replace(refund, "USD", "EUR", 1), false},
		{"amount", report, strings.Replace(refund, "100.00", "99.00", 1), false},
		{"pending", report, strings.Replace(refund, "COMPLETED", "PENDING", 1), false},
		{"wrong capture", report, strings.Replace(refund, "/capture-id", "/other-id", 1), false},
		{"wrong refund", report, strings.Replace(refund, "refund-id", "other-refund", 1), false},
		{"missing report", `{"total_pages":1,"transaction_details":[]}`, refund, false},
		{"missing pages", strings.Replace(report, `"total_pages":1,`, "", 1), refund, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			paymentVerificationClient = &http.Client{Transport: reconciliationRoundTripper(func(req *http.Request) (*http.Response, error) {
				require.Equal(t, "GET", req.Method)
				body := tt.refund
				if strings.HasPrefix(req.URL.Path, "/v1/reporting/") {
					body = tt.report
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			id, err := VerifyPayPalFullRefund(context.Background(), "capture-id", "2026-10-01T06:59:49Z", "100", "USD")
			if tt.ok {
				require.NoError(t, err)
				require.Equal(t, "refund-id", id)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestPayPalDailyStatementIncludesRefundOnlyDaysWithoutDuplicateCapture(t *testing.T) {
	oldClient, oldToken, oldExpiry := paymentVerificationClient, payPalAccessToken, payPalTokenExpiry
	t.Cleanup(func() {
		paymentVerificationClient = oldClient
		payPalAccessToken = oldToken
		payPalTokenExpiry = oldExpiry
	})
	payPalAccessToken = "anonymous-token"
	payPalTokenExpiry = time.Now().Add(time.Hour)
	refund := `{"transaction_info":{"transaction_id":"refund-id","paypal_reference_id":"capture-id","paypal_reference_id_type":"TXN","transaction_event_code":"T1107","transaction_status":"S","transaction_amount":{"value":"-100","currency_code":"USD"}}}`
	payment := `{"transaction_info":{"transaction_id":"capture-id","custom_field":"order","transaction_event_code":"T0006","transaction_status":"S","transaction_amount":{"value":"100","currency_code":"USD"}}}`
	for _, onlyRefund := range []bool{false, true} {
		body := `{"total_pages":1,"transaction_details":[` + refund
		if !onlyRefund {
			body += "," + payment
		}
		body += "]}"
		paymentVerificationClient = &http.Client{Transport: reconciliationRoundTripper(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		rows, err := listPayPalStatement(context.Background(), start, start.Add(24*time.Hour))
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, "capture-id", rows[0].ID)
		require.Equal(t, onlyRefund, rows[0].RefundOnly)
	}
}
