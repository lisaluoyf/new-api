package controller

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/stretchr/testify/require"
)

func TestPayPalUncreditedRefundDailyReconciliation(t *testing.T) {
	for _, tc := range []struct {
		name, status, code, want, problem                                                   string
		priorDay, refundOnly, credited, wrongIdentity, partial, missingRefund, queryFailure bool
	}{
		{name: "same day IPR reversal", status: "REFUNDED", code: "T1116", want: "matched"},
		{name: "prior day pending order IPR reversal", status: "REFUNDED", code: "T1116", priorDay: true, want: "matched"},
		{name: "refund only day IPR reversal", status: "REFUNDED", code: "T1116", priorDay: true, refundOnly: true, want: "matched"},
		{name: "ordinary full refund", status: "REFUNDED", code: "T1107", want: "matched"},
		{name: "actually paid without credit", status: "COMPLETED", code: "T1116", want: "difference", problem: "official_paid_local_not_success"},
		{name: "unrecovered prior credit", status: "REFUNDED", code: "T1116", credited: true, want: "unverified", problem: "refund_requires_separate_funds_and_entitlement_review"},
		{name: "partial refund", status: "REFUNDED", code: "T1116", partial: true, want: "unverified", problem: "refund_requires_separate_funds_and_entitlement_review"},
		{name: "refund endpoint unavailable", status: "REFUNDED", code: "T1116", missingRefund: true, want: "unverified", problem: "refund_requires_separate_funds_and_entitlement_review"},
		{name: "wrong capture identity", status: "REFUNDED", code: "T1116", wrongIdentity: true, want: "difference", problem: "official_identity_mismatch"},
		{name: "capture query unavailable", status: "REFUNDED", code: "T1116", queryFailure: true, want: "unverified", problem: "official_query_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupCryptoPersistenceTestDB(t)
			oldSQLite := common.UsingSQLite
			common.UsingSQLite = true
			t.Cleanup(func() { common.UsingSQLite = oldSQLite })
			require.NoError(t, db.AutoMigrate(&model.SubscriptionOrder{}, &model.PaymentQueryReference{}, &model.PaymentReconciliationJob{}, &model.PaymentReconciliationRun{}))
			day := time.Date(2026, 10, 6, 0, 0, 0, 0, reconciliationTimezone)
			created := day.Unix() + 60
			if tc.priorDay {
				created -= 86400
			}
			top := model.TopUp{TradeNo: "anonymous-wallet-refund", UserId: 1, Money: 100, Amount: 100, PaymentProvider: "paypal", PaymentMethod: "paypal", Status: "pending", CreateTime: created}
			if tc.credited {
				top.CreditedAmount = 100
				top.CompleteTime = created + 30
			}
			require.NoError(t, db.Create(&top).Error)
			require.NoError(t, db.Create(&model.User{Id: 1, Quota: 17}).Error)
			require.NoError(t, db.Create(&model.PaymentQueryReference{TradeNo: top.TradeNo, Provider: "paypal", QueryID: "order:order-wallet", Currency: "USD"}).Error)
			oldID, oldSecret := setting.PayPalClientID, setting.PayPalClientSecret
			setting.PayPalClientID = "test"
			setting.PayPalClientSecret = "test"
			t.Cleanup(func() { setting.PayPalClientID = oldID; setting.PayPalClientSecret = oldSecret })
			if service.GetHttpClient() == nil {
				service.InitHttpClient()
			}
			client := service.GetHttpClient()
			oldTransport := client.Transport
			oldDefaultTransport := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = oldDefaultTransport })
			t.Cleanup(func() { client.Transport = oldTransport })
			captureQueries := 0
			client.Transport = clinkReconciliationTransport(func(r *http.Request) (*http.Response, error) {
				body := ""
				switch r.URL.Path {
				case "/v1/oauth2/token":
					body = `{"access_token":"test-uncredited-refund","expires_in":3600}`
				case "/v1/reporting/transactions":
					receipt := `{"transaction_info":{"transaction_id":"capture-wallet","custom_field":"anonymous-wallet-refund","transaction_event_code":"T0006","transaction_status":"S","transaction_amount":{"value":"100","currency_code":"USD"}}}`
					refund := `{"transaction_info":{"transaction_id":"refund-wallet","custom_field":"anonymous-wallet-refund","paypal_reference_id":"capture-wallet","paypal_reference_id_type":"TXN","transaction_event_code":"` + tc.code + `","transaction_status":"S","transaction_amount":{"value":"-100","currency_code":"USD"}}}`
					if tc.partial {
						refund = strings.Replace(refund, `"value":"-100"`, `"value":"-20"`, 1)
					}
					rows := receipt
					if tc.status == "REFUNDED" {
						rows += "," + refund
					}
					if tc.refundOnly {
						rows = refund
					}
					body = `{"total_pages":1,"transaction_details":[` + rows + `]}`
				case "/v2/checkout/orders/order-wallet":
					body = `{"id":"order-wallet","status":"COMPLETED","purchase_units":[{"custom_id":"anonymous-wallet-refund","amount":{"value":"100","currency_code":"USD"},"payments":{"captures":[{"id":"capture-wallet","status":"` + tc.status + `","amount":{"value":"100","currency_code":"USD"}}]}}]}`
				case "/v2/payments/captures/capture-wallet":
					captureQueries++
					if tc.queryFailure {
						return nil, context.DeadlineExceeded
					}
					body = `{"id":"capture-wallet","status":"` + tc.status + `","custom_id":"anonymous-wallet-refund","amount":{"value":"100","currency_code":"USD"},"update_time":"2026-10-06T02:08:10Z"}`
					if tc.wrongIdentity {
						body = strings.Replace(body, `"custom_id":"anonymous-wallet-refund"`, `"custom_id":"other"`, 1)
					}
				case "/v2/payments/refunds/refund-wallet":
					if tc.missingRefund {
						return nil, context.DeadlineExceeded
					}
					body = `{"id":"refund-wallet","status":"COMPLETED","amount":{"value":"100","currency_code":"USD"},"links":[{"rel":"up","href":"https://api.paypal.com/v2/payments/captures/capture-wallet"}]}`
				default:
					t.Fatalf("unexpected endpoint %s", r.URL.Path)
				}
				if r.URL.Path != "/v1/oauth2/token" {
					require.Equal(t, "GET", r.Method)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})
			http.DefaultTransport = client.Transport
			require.NoError(t, model.QueuePaymentReconciliation(day.Format("2006-01-02"), "paypal", 0, false))
			job, err := model.ClaimPaymentReconciliationJob(time.Now().Unix())
			require.NoError(t, err)
			items, err := reconcilePaymentDay(job)
			require.NoError(t, err)
			require.Len(t, items, 1)
			i := items[0]
			require.Equal(t, tc.want, i.Result)
			require.Equal(t, tc.problem, i.Problem)
			require.Positive(t, captureQueries)
			if tc.want == "matched" {
				require.Equal(t, "refund_matched", i.Verification)
				require.False(t, i.LocalPaid)
				require.False(t, i.OfficialPaid)
				require.Equal(t, "REFUNDED", i.OfficialStatus)
				summary := model.SummarizePaymentReconciliation(items)
				require.Zero(t, summary.DifferenceCount)
				require.Zero(t, summary.UnverifiedCount)
				require.Zero(t, summary.LocalPaidCount)
				require.Zero(t, summary.OfficialPaidCount)
				require.Equal(t, 1, summary.RefundMatchedCount)
				require.Empty(t, summary.Totals)
			}
			var after model.TopUp
			require.NoError(t, db.First(&after, top.Id).Error)
			require.Equal(t, top, after)
			var user model.User
			require.NoError(t, db.First(&user, 1).Error)
			require.Equal(t, 17, user.Quota)
		})
	}
}
