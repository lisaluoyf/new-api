package model

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func setupPlategaEventTest(t *testing.T, status string) VerifiedPlategaState {
	t.Helper()
	oldDB, oldUnit := DB, common.QuotaPerUnit
	oldSQLite := common.UsingSQLite
	oldPostgres := common.UsingPostgreSQL
	var db *gorm.DB
	var err error
	pgDSN := os.Getenv("PLATEGA_TEST_POSTGRES_DSN")
	if pgDSN != "" {
		admin, openErr := gorm.Open(postgres.Open(pgDSN), &gorm.Config{})
		require.NoError(t, openErr)
		schema := fmt.Sprintf("platega_test_%d_%x", os.Getpid(), sha256.Sum256([]byte(t.Name())))[:60]
		require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
		db, err = gorm.Open(postgres.Open(pgDSN+" search_path="+schema), &gorm.Config{})
		t.Cleanup(func() {
			require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
			pool, _ := admin.DB()
			_ = pool.Close()
		})
	} else {
		db, err = gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	}
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}, &TopUp{}, &PlategaOrder{}, &SubscriptionOrder{}, &SubscriptionPlan{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{}, &SubscriptionExpiryRevenue{}, &PlategaEvent{}, &PlategaObservation{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	if pgDSN == "" {
		sqlDB.SetMaxOpenConns(1)
	} else {
		sqlDB.SetMaxOpenConns(12)
	}
	DB, common.QuotaPerUnit = db, 500000
	common.UsingSQLite, common.UsingPostgreSQL = pgDSN == "", pgDSN != ""
	t.Cleanup(func() {
		DB, common.QuotaPerUnit = oldDB, oldUnit
		common.UsingSQLite, common.UsingPostgreSQL = oldSQLite, oldPostgres
		_ = sqlDB.Close()
	})
	require.NoError(t, DB.Create(&User{Id: 1, Username: "anonymous", Quota: 100}).Error)
	require.NoError(t, DB.Create(&TopUp{UserId: 1, TradeNo: "test-order", Amount: 10, Money: 10, PaymentProvider: PaymentProviderPlatega, Status: status}).Error)
	require.NoError(t, DB.Create(&PlategaOrder{UserId: 1, TradeNo: "test-order", Payload: "test-order", PlategaTransactionId: "test-transaction", RubAmount: 100, CallbackHeadersJSON: `{"X-Secret":"private-test-marker"}`}).Error)
	return VerifiedPlategaState{TransactionID: "test-transaction", TradeNo: "test-order", UserID: 1, Status: "CONFIRMED", APIJSON: `{"status":"CONFIRMED"}`}
}

func plategaTestEvent(t *testing.T, payload, status string) *PlategaEvent {
	t.Helper()
	e, err := SavePlategaEvent("test-order", "test-transaction", "callback", "", payload, status, 0)
	require.NoError(t, err)
	return e
}
func plategaQuota(t *testing.T) int {
	t.Helper()
	var u User
	require.NoError(t, DB.First(&u, 1).Error)
	return u.Quota
}

func TestPlategaVerifiedSuccessConcurrentIdempotency(t *testing.T) {
	proof := setupPlategaEventTest(t, common.TopUpStatusPending)
	e := plategaTestEvent(t, "one", "CONFIRMED")
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- ApplyVerifiedPlategaEvent(e.Id, proof) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, 5000100, plategaQuota(t))
	another := plategaTestEvent(t, "two", "CONFIRMED")
	require.NoError(t, ApplyVerifiedPlategaEvent(another.Id, proof))
	require.Equal(t, 5000100, plategaQuota(t))
	var obs []PlategaObservation
	require.NoError(t, DB.Find(&obs).Error)
	require.Len(t, obs, 2)
	require.NotContains(t, obs[0].BeforeStateJSON, "private-test-marker")
}

func TestPlategaCancellationAndDelayedConfirmation(t *testing.T) {
	proof := setupPlategaEventTest(t, common.TopUpStatusPending)
	e := plategaTestEvent(t, "cancel", "CANCELED")
	proof.Status = "CANCELED"
	require.NoError(t, ApplyVerifiedPlategaEvent(e.Id, proof))
	require.Equal(t, 100, plategaQuota(t))
	require.Equal(t, common.TopUpStatusFailed, GetTopUpByTradeNo("test-order").Status)
	proof.Status = "CONFIRMED"
	e = plategaTestEvent(t, "late", "CONFIRMED")
	require.NoError(t, ApplyVerifiedPlategaEvent(e.Id, proof))
	require.Equal(t, 5000100, plategaQuota(t))
	proof.Status = "CANCELED"
	e = plategaTestEvent(t, "cancel-after", "CANCELED")
	require.NoError(t, ApplyVerifiedPlategaEvent(e.Id, proof))
	require.Equal(t, 5000100, plategaQuota(t))
	require.Equal(t, common.TopUpStatusSuccess, GetTopUpByTradeNo("test-order").Status)
	require.NoError(t, DB.First(e, e.Id).Error)
	require.Equal(t, "review", e.Status)
}

func TestPlategaCanceledCallbackConfirmedAPIKeepsCredit(t *testing.T) {
	proof := setupPlategaEventTest(t, common.TopUpStatusSuccess)
	e := plategaTestEvent(t, "counterexample", "CANCELED")
	require.NoError(t, ApplyVerifiedPlategaEvent(e.Id, proof))
	require.Equal(t, 100, plategaQuota(t))
	require.NoError(t, DB.First(e, e.Id).Error)
	require.Equal(t, "recheck", e.Status)
	require.Equal(t, common.TopUpStatusSuccess, GetTopUpByTradeNo("test-order").Status)
}

func TestPlategaRefundAmountEvidencePartialFullAndNegativeBalance(t *testing.T) {
	proof := setupPlategaEventTest(t, common.TopUpStatusSuccess)
	proof.Status = "CHARGEBACKED"
	e := plategaTestEvent(t, "no-evidence", "CHARGEBACKED")
	require.NoError(t, ApplyVerifiedPlategaEvent(e.Id, proof))
	require.Equal(t, 100, plategaQuota(t))
	amount := 40.0
	proof.CumulativeReversalRub = &amount
	proof.ReversalEvidence = "verified-provider-refund-receipt"
	e = plategaTestEvent(t, "partial", "CHARGEBACKED")
	require.NoError(t, ApplyVerifiedPlategaEvent(e.Id, proof))
	require.Equal(t, -1999900, plategaQuota(t))
	e = plategaTestEvent(t, "duplicate-partial", "CHARGEBACKED")
	require.NoError(t, ApplyVerifiedPlategaEvent(e.Id, proof))
	require.Equal(t, -1999900, plategaQuota(t))
	amount = 100
	e = plategaTestEvent(t, "full", "CHARGEBACKED")
	require.NoError(t, ApplyVerifiedPlategaEvent(e.Id, proof))
	require.Equal(t, -4999900, plategaQuota(t))
	require.Equal(t, common.TopUpStatusRefunded, GetTopUpByTradeNo("test-order").Status)
	amount = 40
	e = plategaTestEvent(t, "out-of-order", "CHARGEBACKED")
	require.NoError(t, ApplyVerifiedPlategaEvent(e.Id, proof))
	require.Equal(t, -4999900, plategaQuota(t))
}

func TestPlategaBusinessFailureRollsBackAndRecovers(t *testing.T) {
	proof := setupPlategaEventTest(t, common.TopUpStatusPending)
	e := plategaTestEvent(t, "rollback", "CONFIRMED")
	require.NoError(t, DB.Callback().Create().Before("gorm:create").Register("fail-platega-audit", func(tx *gorm.DB) {
		if tx.Statement.Table == "platega_observations" {
			tx.AddError(errors.New("injected audit failure"))
		}
	}))
	require.Error(t, ApplyVerifiedPlategaEvent(e.Id, proof))
	require.Equal(t, 100, plategaQuota(t))
	require.Equal(t, common.TopUpStatusPending, GetTopUpByTradeNo("test-order").Status)
	require.NoError(t, DB.Callback().Create().Remove("fail-platega-audit"))
	require.NoError(t, RetryPlategaEvent(e.Id, "temporary_business_failure"))
	require.NoError(t, ApplyVerifiedPlategaEvent(e.Id, proof))
	require.Equal(t, 5000100, plategaQuota(t))
}

func TestPlategaCodingSubscriptionUsesEntitlementReversal(t *testing.T) {
	proof := setupPlategaEventTest(t, common.TopUpStatusFailed)
	plan := SubscriptionPlan{Id: 5001, Title: "Test Coding Plan", PlanType: SubscriptionPlanTypeCodingPlan, PriceAmount: 10, Currency: "USD", DurationUnit: SubscriptionDurationDay, DurationValue: 30, Enabled: true, TierLevel: 1, CodingOfficialAmountUSD: 10, TotalAmount: 5000000, CodingModelMultipliers: `{"test-model":1.000}`}
	require.NoError(t, DB.Create(&plan).Error)
	InvalidateSubscriptionPlanCache(plan.Id)
	sub := SubscriptionOrder{UserId: 1, TradeNo: "test-order", PlanId: plan.Id, Money: 10, PaymentProvider: PaymentProviderPlatega, PaymentMethod: PaymentMethodPlatega, OrderType: "purchase", Status: "expired"}
	require.NoError(t, DB.Create(&sub).Error)
	e := plategaTestEvent(t, "late-subscription", "CONFIRMED")
	require.NoError(t, ApplyVerifiedPlategaEvent(e.Id, proof))
	require.Equal(t, 100, plategaQuota(t), "subscription price must never be credited to wallet")
	var entitlement UserSubscription
	require.NoError(t, DB.Where("current_cycle_id = ?", sub.Id).First(&entitlement).Error)
	require.NoError(t, DB.Model(&entitlement).Update("amount_used", 2000000).Error)
	proof.Status = "CHARGEBACKED"
	amount := 40.0
	proof.CumulativeReversalRub = &amount
	proof.ReversalEvidence = "verified-provider-receipt"
	e = plategaTestEvent(t, "partial-subscription", "CHARGEBACKED")
	require.NoError(t, ApplyVerifiedPlategaEvent(e.Id, proof))
	require.NoError(t, DB.First(&entitlement, entitlement.Id).Error)
	require.Equal(t, "active", entitlement.Status)
	amount = 100
	e = plategaTestEvent(t, "full-subscription", "CHARGEBACKED")
	require.NoError(t, ApplyVerifiedPlategaEvent(e.Id, proof))
	require.NoError(t, DB.First(&entitlement, entitlement.Id).Error)
	require.Equal(t, "cancelled", entitlement.Status)
	require.EqualValues(t, 2000000, entitlement.AmountUsed, "consumed entitlement remains in audit")
	require.Equal(t, 100, plategaQuota(t))
	e = plategaTestEvent(t, "duplicate-subscription-refund", "CHARGEBACKED")
	require.NoError(t, ApplyVerifiedPlategaEvent(e.Id, proof))
	require.Equal(t, 100, plategaQuota(t))
}

func TestPlategaDistinctConcurrentSuccessEventsIssueOnce(t *testing.T) {
	proof := setupPlategaEventTest(t, common.TopUpStatusPending)
	var events []*PlategaEvent
	for i := 0; i < 8; i++ {
		events = append(events, plategaTestEvent(t, fmt.Sprintf("distinct-%d", i), "CONFIRMED"))
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(events))
	for _, e := range events {
		wg.Add(1)
		go func(id int) { defer wg.Done(); errs <- ApplyVerifiedPlategaEvent(id, proof) }(e.Id)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, 5000100, plategaQuota(t))
	var observations []PlategaObservation
	require.NoError(t, DB.Find(&observations).Error)
	require.Len(t, observations, 8)
	var total int64
	for _, o := range observations {
		total += o.QuotaDelta
	}
	require.EqualValues(t, 5000000, total)
}

func TestPlategaRefundUsesFrozenBonusAndProtectsOtherFunds(t *testing.T) {
	proof := setupPlategaEventTest(t, common.TopUpStatusPending)
	require.NoError(t, DB.Model(&TopUp{}).Where("trade_no = ?", proof.TradeNo).Update("credited_amount", 12.345678).Error)
	e := plategaTestEvent(t, "precise-bonus", "CONFIRMED")
	require.NoError(t, ApplyVerifiedPlategaEvent(e.Id, proof))
	require.Equal(t, 6172939, plategaQuota(t))
	// Another payment's funds and later presentation edits cannot change the
	// quota originally issued for the order being refunded.
	require.NoError(t, DB.Model(&User{}).Where("id = ?", 1).Update("quota", gorm.Expr("quota + ?", 3000000)).Error)
	require.NoError(t, DB.Model(&TopUp{}).Where("trade_no = ?", proof.TradeNo).Update("credited_amount", 90).Error)
	amount := 100.0
	proof.CumulativeReversalRub = &amount
	proof.ReversalEvidence = "verified-receipt"
	proof.Status = "CHARGEBACK"
	e = plategaTestEvent(t, "legacy-chargeback-alias", "CHARGEBACK")
	require.NoError(t, ApplyVerifiedPlategaEvent(e.Id, proof))
	require.Equal(t, 3000100, plategaQuota(t))
}

func TestPlategaSubscriptionDescendantPreventsAutomaticReversal(t *testing.T) {
	proof := setupPlategaEventTest(t, common.TopUpStatusSuccess)
	sub := SubscriptionOrder{Id: 4001, UserId: 1, TradeNo: proof.TradeNo, Money: 10, PaymentProvider: PaymentProviderPlatega, Status: common.TopUpStatusSuccess}
	require.NoError(t, DB.Create(&sub).Error)
	entitlement := UserSubscription{Id: 4002, UserId: 1, CurrentCycleId: sub.Id, Status: "active", AmountTotal: 5000000, AmountUsed: 1000000}
	require.NoError(t, DB.Create(&entitlement).Error)
	require.NoError(t, DB.Create(&SubscriptionOrder{UserId: 1, TradeNo: "separate-renewal", PreviousSubscriptionId: entitlement.Id, Status: common.TopUpStatusSuccess}).Error)
	amount := 100.0
	proof.CumulativeReversalRub = &amount
	proof.ReversalEvidence = "verified-receipt"
	proof.Status = "CHARGEBACKED"
	e := plategaTestEvent(t, "review-renewal-lineage", "CHARGEBACKED")
	require.NoError(t, ApplyVerifiedPlategaEvent(e.Id, proof))
	require.NoError(t, DB.First(e, e.Id).Error)
	require.Equal(t, "review", e.Status)
	require.NoError(t, DB.First(&entitlement, entitlement.Id).Error)
	require.Equal(t, "active", entitlement.Status)
	require.Equal(t, 100, plategaQuota(t))
}
