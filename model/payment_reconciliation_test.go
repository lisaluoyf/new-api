package model

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupPaymentReconciliationTest(t *testing.T) {
	setupPlategaEventTest(t, "pending")
	require.NoError(t, DB.AutoMigrate(&PaymentQueryReference{}, &PaymentReconciliationJob{}, &PaymentReconciliationRun{}, &PaymentReconciliationItem{}))
}
func TestPaymentReconciliationExactCurrencyTotals(t *testing.T) {
	rows := []PaymentReconciliationItem{
		{LocalPaid: true, LocalAmount: "0.1", Currency: "USD", OfficialPaid: true, OfficialAmount: "0.10", OfficialCurrency: "USD", Result: "matched"},
		{LocalPaid: true, LocalAmount: "0.2", Currency: "USD", OfficialPaid: true, OfficialAmount: "0.20", OfficialCurrency: "USD", Result: "matched"},
		{LocalPaid: true, LocalAmount: "417.75", Currency: "RUB", Result: "difference"},
		{LocalPaid: true, LocalAmount: "1.000001", Currency: "USD", Result: "unverified"},
	}
	r := SummarizePaymentReconciliation(rows)
	require.Equal(t, "incomplete", r.Status)
	require.Equal(t, 2, r.MatchedCount)
	require.Equal(t, 4, r.LocalPaidCount)
	require.Equal(t, 2, r.OfficialPaidCount)
	require.Equal(t, 1, r.DifferenceCount)
	require.Equal(t, 1, r.UnverifiedCount)
	m := map[string]PaymentReconciliationTotal{}
	for _, a := range r.Totals {
		m[a.Currency] = a
	}
	require.Equal(t, "1.300001", m["USD"].LocalAmount)
	require.Equal(t, "0.3", m["USD"].OfficialAmount)
	require.Equal(t, "417.75", m["RUB"].Difference)
}
func TestPaymentReconciliationClaimFinishRerunKeepsHistory(t *testing.T) {
	setupPaymentReconciliationTest(t)
	require.NoError(t, QueuePaymentReconciliation("2026-09-30", "epay", 1, true))
	require.NoError(t, QueuePaymentReconciliation("2026-09-30", "epay", 0, false))
	now := time.Now().Unix()
	j, e := ClaimPaymentReconciliationJob(now)
	require.NoError(t, e)
	require.NotNil(t, j)
	second, e := ClaimPaymentReconciliationJob(now)
	require.NoError(t, e)
	require.Nil(t, second)
	require.Error(t, QueuePaymentReconciliation("2026-09-30", "epay", 1, true))
	rows := []PaymentReconciliationItem{{TradeNo: "anonymous", LocalPaid: true, LocalAmount: "1", Currency: "CNY", OfficialPaid: true, OfficialAmount: "1", OfficialCurrency: "CNY", Result: "matched"}}
	require.NoError(t, FinishPaymentReconciliation(j, rows, false))
	require.Error(t, FinishPaymentReconciliation(j, rows, false))
	require.NoError(t, QueuePaymentReconciliation("2026-09-30", "epay", 1, true))
	next, e := ClaimPaymentReconciliationJob(now)
	require.NoError(t, e)
	require.NotEqual(t, j.RunID, next.RunID)
	require.NoError(t, FinishPaymentReconciliation(next, rows, false))
	var runs int64
	require.NoError(t, DB.Model(&PaymentReconciliationRun{}).Count(&runs).Error)
	require.EqualValues(t, 2, runs)
	var first PaymentReconciliationRun
	require.NoError(t, DB.First(&first, j.RunID).Error)
	require.Equal(t, 1, first.ActorID)
	require.Equal(t, "matched", first.Status)
}
func TestPaymentReconciliationRetriesInterruptedAndAtomicFailure(t *testing.T) {
	setupPaymentReconciliationTest(t)
	now := time.Now().Unix()
	require.NoError(t, QueuePaymentReconciliation("2026-09-30", "platega", 0, false))
	j, e := ClaimPaymentReconciliationJob(now)
	require.NoError(t, e)
	require.NoError(t, DB.Callback().Create().Before("gorm:create").Register("fail-reconciliation-item", func(tx *gorm.DB) {
		if tx.Statement.Table == "payment_reconciliation_items" {
			tx.AddError(gorm.ErrInvalidData)
		}
	}))
	require.Error(t, FinishPaymentReconciliation(j, []PaymentReconciliationItem{{TradeNo: "anonymous", Result: "unverified"}}, false))
	var count int64
	require.NoError(t, DB.Model(&PaymentReconciliationItem{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, DB.Callback().Create().Remove("fail-reconciliation-item"))
	recovered, e := ClaimPaymentReconciliationJob(now + 3601)
	require.NoError(t, e)
	require.NotNil(t, recovered)
	require.NotEqual(t, j.RunID, recovered.RunID)
	require.Error(t, FinishPaymentReconciliation(j, nil, false))
	require.NoError(t, FinishPaymentReconciliation(recovered, []PaymentReconciliationItem{{TradeNo: "anonymous", Result: "unverified", Problem: "official_query_failed"}}, false))
	var queued PaymentReconciliationJob
	require.NoError(t, DB.First(&queued, j.ID).Error)
	require.Equal(t, "queued", queued.Status)
	require.Greater(t, queued.NextAttempt, now)
}
func TestPaymentReconciliationConcurrentClaims(t *testing.T) {
	setupPaymentReconciliationTest(t)
	require.NoError(t, QueuePaymentReconciliation("2026-09-30", "epay", 1, true))
	var wg sync.WaitGroup
	ch := make(chan *PaymentReconciliationJob, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			j, e := ClaimPaymentReconciliationJob(time.Now().Unix())
			if e != nil {
				errs <- e
			}
			if j != nil {
				ch <- j
			}
		}()
	}
	wg.Wait()
	close(ch)
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	require.Len(t, ch, 1)
}

func TestPaymentReconciliationSkipsDisabledHistoricalJobs(t *testing.T) {
	setupPaymentReconciliationTest(t)
	for _, provider := range []string{"waffo", "unknown", "stripe", "creem"} {
		require.NoError(t, DB.Create(&PaymentReconciliationJob{Day: "2026-10-01", Provider: provider, Status: "queued"}).Error)
		require.Error(t, QueuePaymentReconciliation("2026-09-30", provider, 1, true))
	}
	require.NoError(t, QueuePaymentReconciliation("2026-09-30", "waffo_pancake", 1, true))
	job, err := ClaimPaymentReconciliationJob(time.Now().Unix())
	require.NoError(t, err)
	require.NotNil(t, job)
	require.Equal(t, "waffo_pancake", job.Provider)
	next, err := ClaimPaymentReconciliationJob(time.Now().Unix())
	require.NoError(t, err)
	require.Nil(t, next)
	var count int64
	require.NoError(t, DB.Model(&PaymentReconciliationJob{}).Where("provider IN ? AND status = ? AND attempts = 0", []string{"waffo", "unknown", "stripe", "creem"}, "queued").Count(&count).Error)
	require.EqualValues(t, 4, count)
}

func TestPaymentReconciliationCoverageDoesNotCountAsAnOrder(t *testing.T) {
	result := SummarizePaymentReconciliation([]PaymentReconciliationItem{
		{Purpose: "wallet", Result: "matched"},
		{Purpose: "wallet", Result: "matched"},
		{Purpose: "wallet", Result: "unverified"},
		{Purpose: "coverage", Result: "unverified"},
	})
	require.Equal(t, 2, result.MatchedCount)
	require.Equal(t, 3, result.CheckedCount)
	require.Equal(t, 2, result.UnverifiedCount)
	require.Equal(t, "incomplete", result.Status)
}

func TestLocalOnlyReconciliationPreservesOrderFailures(t *testing.T) {
	for _, provider := range []string{"crypto", "nowpayments"} {
		for _, result := range []string{"matched", "difference", "unverified"} {
			t.Run(provider+"/"+result, func(t *testing.T) {
				setupPaymentReconciliationTest(t)
				require.NoError(t, QueuePaymentReconciliation("2026-09-30", provider, 1, true))
				job, err := ClaimPaymentReconciliationJob(time.Now().Unix())
				require.NoError(t, err)
				require.NoError(t, FinishPaymentReconciliation(job, []PaymentReconciliationItem{{Purpose: "wallet", Result: result}}, false))
				var run PaymentReconciliationRun
				require.NoError(t, DB.First(&run, job.RunID).Error)
				require.Equal(t, "local_successful_orders_only", run.Coverage)
				expected := result
				if result == "unverified" {
					expected = "incomplete"
				}
				require.Equal(t, expected, run.Status)
				require.Equal(t, 1, run.CheckedCount)
			})
		}
	}
	require.False(t, PaymentReconciliationLocalOnly("paypal"))
	require.False(t, PaymentReconciliationLocalOnly("epay"))
}

func TestManualReconciliationConfirmationPreservesEvidenceAndReruns(t *testing.T) {
	setupPaymentReconciliationTest(t)
	require.NoError(t, QueuePaymentReconciliation("2026-10-01", "epay", 1, true))
	job, err := ClaimPaymentReconciliationJob(time.Now().Unix())
	require.NoError(t, err)
	rows := []PaymentReconciliationItem{
		{TradeNo: "manual-order", Purpose: "wallet", LocalPaid: true, LocalAmount: "10", Currency: "CNY", OfficialPaid: true, OfficialAmount: "9", OfficialCurrency: "CNY", Result: "difference", Problem: "amount_mismatch"},
		{TradeNo: "unverified-order", Purpose: "wallet", Result: "unverified", Problem: "official_query_failed"},
	}
	require.NoError(t, FinishPaymentReconciliation(job, rows, false))
	var item PaymentReconciliationItem
	require.NoError(t, DB.Where("run_id = ? AND trade_no = ?", job.RunID, "manual-order").First(&item).Error)
	require.NoError(t, ConfirmPaymentReconciliationItem(item.ID, 7))
	require.NoError(t, ConfirmPaymentReconciliationItem(item.ID, 8))
	require.NoError(t, DB.First(&item, item.ID).Error)
	require.Equal(t, 7, item.ManualConfirmedBy)
	require.Positive(t, item.ManualConfirmedAt)
	require.Equal(t, "difference", item.Result)
	require.Equal(t, "amount_mismatch", item.Problem)
	require.Equal(t, "9", item.OfficialAmount)
	var run PaymentReconciliationRun
	require.NoError(t, DB.First(&run, job.RunID).Error)
	require.Zero(t, run.DifferenceCount)
	require.Equal(t, 1, run.UnverifiedCount)
	require.Equal(t, "incomplete", run.Status)
	var other PaymentReconciliationItem
	require.NoError(t, DB.Where("run_id = ? AND trade_no = ?", job.RunID, "unverified-order").First(&other).Error)
	require.NoError(t, ConfirmPaymentReconciliationItem(other.ID, 7))
	require.NoError(t, DB.First(&run, job.RunID).Error)
	require.Equal(t, "matched", run.Status)
	require.Zero(t, run.UnverifiedCount)
	var finishedJob PaymentReconciliationJob
	require.NoError(t, DB.First(&finishedJob, job.ID).Error)
	require.Equal(t, "done", finishedJob.Status)

	require.NoError(t, QueuePaymentReconciliation(job.Day, job.Provider, 1, true))
	next, err := ClaimPaymentReconciliationJob(time.Now().Unix())
	require.NoError(t, err)
	require.ErrorIs(t, ConfirmPaymentReconciliationItem(item.ID, 7), ErrReconciliationReviewConflict)
	require.NoError(t, FinishPaymentReconciliation(next, rows, false))
	run = PaymentReconciliationRun{}
	require.NoError(t, DB.First(&run, next.RunID).Error)
	require.Equal(t, "matched", run.Status)
	var copied PaymentReconciliationItem
	require.NoError(t, DB.Where("run_id = ? AND trade_no = ?", next.RunID, "manual-order").First(&copied).Error)
	require.Equal(t, item.ManualConfirmedAt, copied.ManualConfirmedAt)
	require.Equal(t, 7, copied.ManualConfirmedBy)

	require.NoError(t, QueuePaymentReconciliation(job.Day, job.Provider, 1, true))
	changed, err := ClaimPaymentReconciliationJob(time.Now().Unix())
	require.NoError(t, err)
	rows[0].OfficialAmount = "8"
	require.NoError(t, FinishPaymentReconciliation(changed, rows, false))
	run = PaymentReconciliationRun{}
	require.NoError(t, DB.First(&run, changed.RunID).Error)
	require.Equal(t, "difference", run.Status)
	require.Equal(t, 1, run.DifferenceCount)
}

func TestManualReconciliationRejectsCoverageAndRunningJobs(t *testing.T) {
	setupPaymentReconciliationTest(t)
	require.ErrorIs(t, ConfirmPaymentReconciliationItem(0, 1), ErrReconciliationReviewInvalid)
	require.ErrorIs(t, ConfirmPaymentReconciliationItem(1, 0), ErrReconciliationReviewInvalid)
	require.NoError(t, QueuePaymentReconciliation("2026-10-01", "epay", 1, true))
	job, err := ClaimPaymentReconciliationJob(time.Now().Unix())
	require.NoError(t, err)
	pending := PaymentReconciliationItem{RunID: job.RunID, TradeNo: "order", Result: "difference"}
	require.NoError(t, DB.Create(&pending).Error)
	require.ErrorIs(t, ConfirmPaymentReconciliationItem(pending.ID, 1), ErrReconciliationReviewConflict)
	require.NoError(t, DB.Delete(&pending).Error)
	require.NoError(t, FinishPaymentReconciliation(job, []PaymentReconciliationItem{{Purpose: "coverage", Result: "unverified", Problem: "official_statement_unavailable"}, {TradeNo: "matched", Result: "matched"}}, false))
	var items []PaymentReconciliationItem
	require.NoError(t, DB.Where("run_id = ?", job.RunID).Find(&items).Error)
	for _, item := range items {
		require.ErrorIs(t, ConfirmPaymentReconciliationItem(item.ID, 1), ErrReconciliationReviewInvalid)
	}
}

func TestManualReconciliationConfirmationRollsBackOnSummaryFailure(t *testing.T) {
	setupPaymentReconciliationTest(t)
	require.NoError(t, QueuePaymentReconciliation("2026-10-01", "epay", 1, true))
	job, err := ClaimPaymentReconciliationJob(time.Now().Unix())
	require.NoError(t, err)
	require.NoError(t, FinishPaymentReconciliation(job, []PaymentReconciliationItem{{TradeNo: "atomic-order", Purpose: "wallet", Result: "difference"}}, false))
	var item PaymentReconciliationItem
	require.NoError(t, DB.Where("run_id = ?", job.RunID).First(&item).Error)
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register("fail-manual-summary", func(tx *gorm.DB) {
		if tx.Statement.Table == "payment_reconciliation_runs" {
			tx.AddError(gorm.ErrInvalidData)
		}
	}))
	t.Cleanup(func() { _ = DB.Callback().Update().Remove("fail-manual-summary") })
	require.Error(t, ConfirmPaymentReconciliationItem(item.ID, 7))
	require.NoError(t, DB.First(&item, item.ID).Error)
	require.Zero(t, item.ManualConfirmedAt)
	require.Zero(t, item.ManualConfirmedBy)
	var run PaymentReconciliationRun
	require.NoError(t, DB.First(&run, job.RunID).Error)
	require.Equal(t, 1, run.DifferenceCount)
	require.Equal(t, "difference", run.Status)
}
