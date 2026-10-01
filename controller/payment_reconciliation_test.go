package controller

import (
	"context"
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
	require.Len(t, rows, 2)
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
