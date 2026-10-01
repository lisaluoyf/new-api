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
