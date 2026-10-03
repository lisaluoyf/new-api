package controller

import (
	"context"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
)

func TestUpstreamSuccessWithoutAnyLocalOrderIsAnIssue(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionOrder{}))
	upstream := []service.StatementPayment{{ID: "anonymous-official", TradeNo: "anonymous-missing", Status: "SUCCESS", Amount: "12.34", Currency: "CNY"}, {ID: "anonymous-official", TradeNo: "anonymous-missing", Status: "SUCCESS", Amount: "12.34", Currency: "CNY"}}
	items, e := mergeReconciliationStatement("epay", nil, upstream)
	require.NoError(t, e)
	require.Len(t, items, 1)
	require.Equal(t, "official_paid_local_order_missing", items[0].Problem)
	require.Equal(t, "missing", items[0].LocalStatus)
	summary := model.SummarizePaymentReconciliation(items)
	require.Equal(t, 1, summary.OfficialPaidCount)
	require.Zero(t, summary.LocalPaidCount)
	require.Equal(t, "-12.34", summary.Totals[0].Difference)
	items, e = mergeReconciliationStatement("epay", items, upstream)
	require.NoError(t, e)
	require.Len(t, items, 1)
}

func TestPaymentReconciliationClassifiesMoneyAndStates(t *testing.T) {
	base := reconciliationCandidate{trade: "anonymous-order", provider: "platega", status: "success", currency: "RUB", money: 100, user: 1}
	proof := reconciliationProof{id: "anonymous-transaction", status: "CONFIRMED", known: true, paid: true, currency: "RUB", amount: "100.00"}
	tests := []struct {
		name            string
		edit            func(*reconciliationCandidate, *reconciliationProof)
		result, problem string
	}{
		{"success", func(_ *reconciliationCandidate, _ *reconciliationProof) {}, "matched", ""},
		{"counterexample ignores stale cancellation", func(_ *reconciliationCandidate, _ *reconciliationProof) {}, "matched", ""},
		{"cancelled after local success", func(_ *reconciliationCandidate, p *reconciliationProof) { p.status = "CANCELED"; p.paid = false }, "difference", "local_success_official_not_paid"},
		{"missed success", func(r *reconciliationCandidate, _ *reconciliationProof) { r.status = "failed" }, "difference", "official_paid_local_not_success"},
		{"pending cancellation", func(r *reconciliationCandidate, p *reconciliationProof) {
			r.status = "pending"
			p.status = "CANCELED"
			p.paid = false
		}, "matched", ""},
		{"wrong amount", func(_ *reconciliationCandidate, p *reconciliationProof) { p.amount = "101" }, "difference", "amount_mismatch"},
		{"wrong currency", func(_ *reconciliationCandidate, p *reconciliationProof) { p.currency = "USD" }, "difference", "currency_mismatch"},
		{"missing amount", func(_ *reconciliationCandidate, p *reconciliationProof) { p.amount = "" }, "unverified", "missing_official_amount"},
		{"invalid amount", func(_ *reconciliationCandidate, p *reconciliationProof) { p.amount = "not-money" }, "unverified", "invalid_official_amount"},
		{"zero amount", func(_ *reconciliationCandidate, p *reconciliationProof) { p.amount = "0" }, "unverified", "invalid_official_amount"},
		{"timeout", func(_ *reconciliationCandidate, p *reconciliationProof) {
			p.known = false
			p.problem = "official_query_failed"
		}, "unverified", "official_query_failed"},
		{"missing query id", func(_ *reconciliationCandidate, p *reconciliationProof) {
			p.known = false
			p.problem = "missing_official_transaction_id"
		}, "unverified", "missing_official_transaction_id"},
		{"unknown state", func(_ *reconciliationCandidate, p *reconciliationProof) { p.status = "MAYBE_PAID" }, "unverified", "unknown_official_status"},
		{"chargeback", func(_ *reconciliationCandidate, p *reconciliationProof) { p.status = "CHARGEBACKED"; p.paid = false }, "unverified", "refund_requires_separate_funds_and_entitlement_review"},
		{"wrong identity", func(_ *reconciliationCandidate, p *reconciliationProof) { p.problem = "official_identity_mismatch" }, "difference", "official_identity_mismatch"},
		{"subscription", func(r *reconciliationCandidate, _ *reconciliationProof) { r.purpose = "subscription" }, "matched", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, p := base, proof
			tt.edit(&r, &p)
			i := classifyReconciliationOrder(r, p)
			require.Equal(t, tt.result, i.Result)
			require.Equal(t, tt.problem, i.Problem)
		})
	}
}

func TestPayPalRefundReconciliationRequiresRefundAndQuotaEvidence(t *testing.T) {
	r := reconciliationCandidate{trade: "anonymous", provider: "paypal", status: "refunded", money: 100, currency: "USD"}
	p := reconciliationProof{id: "capture", status: "REFUNDED", known: true, amount: "100", currency: "USD", refundVerified: true, refundRecoveryVerified: true}
	i := classifyReconciliationOrder(r, p)
	require.Equal(t, "matched", i.Result)
	require.Equal(t, "refund_matched", i.Verification)
	require.False(t, i.LocalPaid)
	require.False(t, i.OfficialPaid)
	p.refundVerified = false
	require.Equal(t, "unverified", classifyReconciliationOrder(r, p).Result)
	p.refundVerified = true
	p.refundRecoveryVerified = false
	require.Equal(t, "unverified", classifyReconciliationOrder(r, p).Result)
	p.refundRecoveryVerified = true
	p.currency = "EUR"
	require.Equal(t, "unverified", classifyReconciliationOrder(r, p).Result)
}

func TestPriorPlategaStatementCannotMaskMissingOrDifferentPayment(t *testing.T) {
	day := time.Date(2026, 10, 1, 0, 0, 0, 0, reconciliationTimezone)
	created := day.AddDate(0, 0, -4).Unix()
	i := model.PaymentReconciliationItem{TradeNo: "order", OfficialID: "provider-id", OfficialCurrency: "RUB", OfficialAmount: "453.26"}
	p := service.StatementPayment{ID: "provider-id", TradeNo: "order", Status: "CONFIRMED", Currency: "RUB", Amount: "453.260", CreatedAt: created}
	i.OfficialAmount = "417.75"
	require.True(t, matchesPriorPlategaStatement(i, created, day, p, "453.26"))
	require.False(t, matchesPriorPlategaStatement(i, created, day, p, "417.75"))
	p.Amount = "450"
	require.False(t, matchesPriorPlategaStatement(i, created, day, p, "453.26"))
	p.Amount = "453.26"
	p.ID = "different"
	require.False(t, matchesPriorPlategaStatement(i, created, day, p, "453.26"))
	p.ID = "provider-id"
	p.CreatedAt = day.Unix()
	require.False(t, matchesPriorPlategaStatement(i, created, day, p, "453.26"))
	require.False(t, matchesPriorPlategaStatement(i, day.Unix(), day, p, "453.26"))
}

func TestPlategaLocalCohortIncludesCreationDayAndLaterLocalCredit(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionOrder{}, &model.PaymentQueryReference{}, &model.PlategaOrder{}))
	day := time.Date(2026, 10, 1, 0, 0, 0, 0, reconciliationTimezone).Unix()
	for _, top := range []model.TopUp{
		{TradeNo: "prior-created", PaymentProvider: "platega", Status: "success", Money: 1, CreateTime: day - 86400, CompleteTime: day + 1},
		{TradeNo: "created-today", PaymentProvider: "platega", Status: "success", Money: 1, CreateTime: day + 1, CompleteTime: day + 86401},
		{TradeNo: "other-day", PaymentProvider: "platega", Status: "success", Money: 1, CreateTime: day - 86400, CompleteTime: day - 1},
	} {
		require.NoError(t, db.Create(&top).Error)
	}
	rows, err := loadReconciliationCandidates(day, day+86400, "platega")
	require.NoError(t, err)
	require.Len(t, rows, 2)
}
func TestReconciliationDateRangeAndProviderValidation(t *testing.T) {
	a, b, e := reconciliationRange("", "")
	require.NoError(t, e)
	require.Equal(t, a, b)
	require.Equal(t, time.Now().In(reconciliationTimezone).AddDate(0, 0, -1).Format("2006-01-02"), a.Format("2006-01-02"))
	_, offset := a.Zone()
	require.Equal(t, 8*3600, offset)
	for _, pair := range [][2]string{{"2026-02-30", "2026-03-01"}, {"2026-01-01", "2026-01-02';delete"}, {"2026-01-02", "2026-01-01"}, {"2026-01-01", "2026-02-02"}} {
		_, _, e = reconciliationRange(pair[0], pair[1])
		require.Error(t, e)
	}
	_, e = reconciliationProviders("malicious-provider")
	require.Error(t, e)
}
func TestReconciliationReadsWalletAndSubscriptionsWithoutDuplicateOrMutation(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionOrder{}, &model.PaymentQueryReference{}, &model.PlategaOrder{}))
	start := time.Date(2026, 9, 30, 0, 0, 0, 0, reconciliationTimezone).Unix()
	end := start + 86400
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "anonymous", Quota: 987654}).Error)
	for _, row := range []model.TopUp{
		{TradeNo: "wallet", UserId: 1, Money: 10, Status: "success", PaymentProvider: "epay", PaymentMethod: "alipay", CreateTime: start - 100, CompleteTime: start + 1},
		{TradeNo: "subscription", UserId: 1, Money: 69, Status: "success", PaymentProvider: "platega", CreateTime: start + 1, CompleteTime: start + 2},
		{TradeNo: "late-next-day", UserId: 1, Money: 1, Status: "success", PaymentProvider: "epay", CreateTime: start + 2, CompleteTime: end + 1},
		{TradeNo: "missed", UserId: 1, Money: 5, Status: "failed", PaymentProvider: "epay", CreateTime: start + 4},
	} {
		require.NoError(t, db.Create(&row).Error)
	}
	require.NoError(t, db.Create(&model.SubscriptionOrder{TradeNo: "subscription", UserId: 1, Money: 69, Status: "success", PaymentProvider: "platega", CreateTime: start + 1, CompleteTime: start + 2}).Error)
	require.NoError(t, db.Create(&model.PlategaOrder{TradeNo: "subscription", RubAmount: 6900}).Error)
	rows, e := loadReconciliationCandidates(start, end, "epay")
	require.NoError(t, e)
	require.Len(t, rows, 1)
	require.Equal(t, "wallet", rows[0].trade)
	rows, e = loadReconciliationCandidates(start, end, "platega")
	require.NoError(t, e)
	require.Len(t, rows, 1)
	require.Equal(t, "subscription", rows[0].purpose)
	require.Equal(t, "RUB", rows[0].currency)
	require.Equal(t, float64(6900), rows[0].money)
	var u model.User
	require.NoError(t, db.First(&u, 1).Error)
	require.Equal(t, 987654, u.Quota)
}
func TestReconciliationUnidentifiedProviderIsNotMatched(t *testing.T) {
	p := queryReconciliationOrder(context.Background(), reconciliationCandidate{provider: "unknown"})
	require.False(t, p.known)
	require.Equal(t, "unknown_payment_provider", p.problem)
}

func TestUpstreamSuccessFindsFailedOrderOutsideLocalDay(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionOrder{}, &model.PaymentQueryReference{}))
	require.NoError(t, db.Create(&model.TopUp{TradeNo: "anonymous-old-order", PaymentProvider: "epay", PaymentMethod: "wxpay", Status: "failed", Money: 10, UserId: 123, CreateTime: 1}).Error)
	payments := []service.StatementPayment{{ID: "anonymous-fresh-paid", TradeNo: "anonymous-old-order", Status: "SUCCESS", Amount: "10", Currency: "CNY"}}
	items, e := mergeReconciliationStatement("epay", nil, payments)
	require.NoError(t, e)
	require.Len(t, items, 1)
	require.Equal(t, "official_paid_local_not_success", items[0].Problem)
	require.Equal(t, 123, items[0].UserID)
	payments = append(payments, service.StatementPayment{ID: "another-paid-id", TradeNo: "anonymous-old-order", Status: "SUCCESS", Amount: "10", Currency: "CNY"})
	items, e = mergeReconciliationStatement("epay", nil, payments)
	require.NoError(t, e)
	require.Len(t, items, 2)
	require.Equal(t, "official_duplicate_payment_for_order", items[1].Problem)
}

func TestReconciliationSuccessUnionExcludesUnpaidButFindsUpstreamSuccess(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionOrder{}, &model.PaymentQueryReference{}))
	start := time.Date(2026, 9, 30, 0, 0, 0, 0, reconciliationTimezone).Unix()
	end := start + 86400
	for _, r := range []model.TopUp{
		{TradeNo: "paid", Money: 10, Status: "success", PaymentProvider: "epay", CompleteTime: start, CreateTime: start - 86400},
		{TradeNo: "pending", Money: 10, Status: "pending", PaymentProvider: "epay", CreateTime: start},
		{TradeNo: "failed", Money: 10, Status: "failed", PaymentProvider: "epay", CreateTime: start},
		{TradeNo: "canceled", Money: 10, Status: "canceled", PaymentProvider: "epay", CreateTime: start},
		{TradeNo: "refunded", Money: 10, Status: "refunded", PaymentProvider: "epay", CompleteTime: start, CreateTime: start},
		{TradeNo: "next-day", Money: 10, Status: "success", PaymentProvider: "epay", CompleteTime: end, CreateTime: start},
	} {
		require.NoError(t, db.Create(&r).Error)
	}
	rows, err := loadReconciliationCandidates(start, end, "epay")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "paid", rows[0].trade)
	items := []model.PaymentReconciliationItem{classifyReconciliationOrder(rows[0], reconciliationProof{id: "p1", status: "CLOSED", known: true})}
	items, err = mergeReconciliationStatement("epay", items, []service.StatementPayment{
		{ID: "p2", TradeNo: "pending", Status: "SUCCESS", Amount: "10", Currency: "CNY"},
		{ID: "p3", TradeNo: "failed", Status: "SUCCESS", Amount: "10", Currency: "CNY"},
		{ID: "p4", TradeNo: "missing", Status: "SUCCESS", Amount: "10", Currency: "CNY"},
	})
	require.NoError(t, err)
	require.Len(t, items, 4)
	summary := model.SummarizePaymentReconciliation(items)
	require.Equal(t, 1, summary.LocalPaidCount)
	require.Equal(t, 3, summary.OfficialPaidCount)
	require.Equal(t, 4, summary.DifferenceCount)
	for _, i := range items {
		require.True(t, i.LocalPaid || i.OfficialPaid)
	}
}

func TestReconciliationUpstreamSuccessRemainsInScopeWhenSecondQueryUnavailable(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionOrder{}, &model.PaymentQueryReference{}, &model.PlategaOrder{}))
	require.NoError(t, db.Create(&model.TopUp{TradeNo: "anonymous-missing-query-id", Status: "failed", PaymentProvider: "platega", Money: 5}).Error)
	items, err := mergeReconciliationStatement("platega", nil, []service.StatementPayment{{ID: "anonymous-paid-id", TradeNo: "anonymous-missing-query-id", Status: "CONFIRMED", Amount: "453.26", Currency: "RUB"}})
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.False(t, items[0].LocalPaid)
	require.True(t, items[0].OfficialPaid)
	require.Equal(t, "unverified", items[0].Result)
	require.Equal(t, "missing_official_transaction_id", items[0].Problem)
	require.Equal(t, 1, model.SummarizePaymentReconciliation(items).OfficialPaidCount)
}

func TestReconciliationEnabledProvidersAndHistoricalMethods(t *testing.T) {
	providers, err := reconciliationProviders("all")
	require.NoError(t, err)
	require.Contains(t, providers, "waffo_pancake")
	for _, provider := range []string{"waffo", "unknown", "stripe", "creem"} {
		require.NotContains(t, providers, provider)
		_, err := reconciliationProviders(provider)
		require.Error(t, err)
	}
	for _, method := range []string{"waffo", "stripe", "creem"} {
		require.Equal(t, method, normalizeReconciliationProvider("", method))
	}
	require.Equal(t, "unknown", normalizeReconciliationProvider("", "unrecognized-test-method"))
}

func TestReconciliationHistoricalMatchedCountExcludesCoverageAndPagination(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.PaymentReconciliationRun{}, &model.PaymentReconciliationJob{}, &model.PaymentReconciliationItem{}))
	run := model.PaymentReconciliationRun{Day: "2026-09-30", Provider: "epay", CheckedCount: 3, UnverifiedCount: 2, Status: "incomplete"}
	require.NoError(t, db.Create(&run).Error)
	require.NoError(t, db.Create(&model.PaymentReconciliationJob{Day: run.Day, Provider: run.Provider, RunID: run.ID, Status: "done"}).Error)
	for _, item := range []model.PaymentReconciliationItem{
		{RunID: run.ID, Result: "matched", Purpose: "wallet"},
		{RunID: run.ID, Result: "matched", Purpose: "wallet"},
		{RunID: run.ID, Result: "unverified", Purpose: "wallet"},
		{RunID: run.ID, Result: "unverified", Purpose: "coverage"},
	} {
		require.NoError(t, db.Create(&item).Error)
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("GET", "/api/payment-reconciliation/?start_date=2026-09-30&end_date=2026-09-30&provider=all&page=2", nil)
	GetPaymentReconciliation(ctx)
	require.Equal(t, 200, recorder.Code)
	var response struct {
		Success       bool
		ProviderModes map[string]string `json:"provider_modes"`
		Runs          []model.PaymentReconciliationRun
		Items         []model.PaymentReconciliationItem
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Equal(t, "local_successful_orders_only", response.ProviderModes["crypto"])
	require.Equal(t, "local_successful_orders_only", response.ProviderModes["nowpayments"])
	require.Equal(t, "bidirectional_official_statement", response.ProviderModes["paypal"])
	require.Len(t, response.Runs, 1)
	require.Equal(t, 2, response.Runs[0].MatchedCount)
	require.Equal(t, 3, response.Runs[0].CheckedCount)
	require.Empty(t, response.Items)
}

func TestLocalOnlyEmptyReconciliationDoesNotRequireStatement(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionOrder{}, &model.PaymentQueryReference{}))
	for _, provider := range []string{"crypto", "nowpayments"} {
		items, err := reconcilePaymentDay(&model.PaymentReconciliationJob{Day: "2026-09-30", Provider: provider})
		require.NoError(t, err)
		require.Empty(t, items)
		require.Equal(t, "matched", model.SummarizePaymentReconciliation(items).Status)
	}
	t.Setenv("EPAY_QUERY_BASE_URL", "")
	items, err := reconcilePaymentDay(&model.PaymentReconciliationJob{Day: "2026-09-30", Provider: "epay"})
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "official_statement_unavailable", items[0].Problem)
	require.Equal(t, "incomplete", model.SummarizePaymentReconciliation(items).Status)
}

func TestClinkReconciliationUsesStableOrderAndOriginalCurrency(t *testing.T) {
	old := queryClinkReconciliationOrder
	t.Cleanup(func() { queryClinkReconciliationOrder = old })
	candidate := reconciliationCandidate{trade: "CLINK-test", provider: "clink", purpose: "wallet", status: "success", queryID: "order_test", clinkSessionID: "sess_expired", currency: "USD", money: 1}
	tests := []struct {
		name, trade, order, session, currency string
		amount                                float64
		result, problem                       string
	}{
		{"expired session paid in INR", "CLINK-test", "order_test", "sess_expired", "USD", 1, "matched", ""},
		{"wrong merchant reference", "other", "order_test", "sess_expired", "USD", 1, "difference", "official_identity_mismatch"},
		{"wrong order", "CLINK-test", "order_other", "sess_expired", "USD", 1, "difference", "official_identity_mismatch"},
		{"wrong session", "CLINK-test", "order_test", "sess_other", "USD", 1, "difference", "official_identity_mismatch"},
		{"missing session", "CLINK-test", "order_test", "", "USD", 1, "difference", "official_identity_mismatch"},
		{"wrong amount", "CLINK-test", "order_test", "sess_expired", "USD", 2, "difference", "amount_mismatch"},
		{"wrong original currency", "CLINK-test", "order_test", "sess_expired", "INR", 1, "difference", "currency_mismatch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queryClinkReconciliationOrder = func(ctx context.Context, id string) (*service.ClinkOrderWebhookData, error) {
				require.Equal(t, "order_test", id)
				return &service.ClinkOrderWebhookData{OrderID: tt.order, SessionID: tt.session, MerchantReferenceID: tt.trade, Status: "success", AmountSubtotal: tt.amount, AmountTotal: 117.83, OriginalCurrency: tt.currency, PaymentCurrency: "INR"}, nil
			}
			item := classifyReconciliationOrder(candidate, queryReconciliationOrder(context.Background(), candidate))
			require.Equal(t, tt.result, item.Result)
			require.Equal(t, tt.problem, item.Problem)
		})
	}
	queryClinkReconciliationOrder = func(context.Context, string) (*service.ClinkOrderWebhookData, error) {
		return nil, context.DeadlineExceeded
	}
	item := classifyReconciliationOrder(candidate, queryReconciliationOrder(context.Background(), candidate))
	require.Equal(t, "unverified", item.Result)
	require.Equal(t, "official_query_failed", item.Problem)
}

type clinkReconciliationTransport func(*http.Request) (*http.Response, error)

func (f clinkReconciliationTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestClinkDailyReconciliationNeverQueriesExpiredSession(t *testing.T) {
	db := setupCryptoPersistenceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SubscriptionOrder{}, &model.PaymentQueryReference{}, &model.PaymentReconciliationJob{}, &model.PaymentReconciliationRun{}))
	day := time.Date(2026, 10, 2, 0, 0, 0, 0, reconciliationTimezone)
	require.NoError(t, db.Create(&model.TopUp{TradeNo: "CLINK-test", PaymentProvider: "clink", Status: "success", Money: 1, CreateTime: day.Unix() + 1, CompleteTime: day.Unix() + 2}).Error)
	require.NoError(t, db.Create(&model.PaymentQueryReference{TradeNo: "CLINK-test", Provider: "clink", QueryID: "sess_expired", Currency: "USD"}).Error)
	require.NoError(t, model.QueuePaymentReconciliation(day.Format("2006-01-02"), "clink", 0, false))
	job, err := model.ClaimPaymentReconciliationJob(time.Now().Unix())
	require.NoError(t, err)
	require.NotNil(t, job)
	t.Setenv("CLINK_SECRET_KEY", "test-secret")
	old := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = old })
	calls := []string{}
	http.DefaultTransport = clinkReconciliationTransport(func(r *http.Request) (*http.Response, error) {
		calls = append(calls, r.URL.Path)
		var body string
		switch r.URL.Path {
		case "/api/order":
			body = `{"code":200,"total":1,"rows":[{"orderId":"order_test","merchantReferenceId":"CLINK-test","status":"success","amountSubtotal":1,"originalCurrency":"USD","paymentTime":` + fmt.Sprint((day.Unix()+2)*1000) + `}]}`
		case "/api/order/order_test":
			body = `{"code":200,"data":{"orderId":"order_test","sessionId":"sess_expired","merchantReferenceId":"CLINK-test","status":"success","amountSubtotal":1,"amountTotal":117.83,"originalCurrency":"USD","paymentCurrency":"INR"}}`
		default:
			t.Fatalf("unexpected query, expired sessions must not be queried: %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	items, err := reconcilePaymentDay(job)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "matched", items[0].Result)
	require.Equal(t, "order_test", items[0].OfficialID)
	require.Equal(t, []string{"/api/order", "/api/order/order_test"}, calls)
	var top model.TopUp
	require.NoError(t, db.Where("trade_no = ?", "CLINK-test").First(&top).Error)
	require.Equal(t, "success", top.Status)
	require.Equal(t, float64(1), top.Money)
}
