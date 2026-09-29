package model

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestNowPaymentsPostgresProductionMigration(t *testing.T) {
	dsn := os.Getenv("NOWPAYMENTS_MIGRATION_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set NOWPAYMENTS_MIGRATION_TEST_POSTGRES_DSN to an isolated database")
	}
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{PrepareStmt: true})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Close() })
	for range 2 {
		require.NoError(t, autoMigrateWithLimits(db, &NowPaymentsPayment{}, &NowPaymentsAttempt{}, &NowPaymentsReconcileState{}))
	}
	require.True(t, db.Migrator().HasTable(&NowPaymentsAttempt{}))
	require.True(t, db.Migrator().HasTable(&NowPaymentsReconcileState{}))
	require.True(t, db.Migrator().HasIndex(&NowPaymentsAttempt{}, "idx_now_payments_attempts_payment_id"))
	require.True(t, db.Migrator().HasColumn(&NowPaymentsAttempt{}, "alert_claim_until"))
	var lockTimeout string
	require.NoError(t, db.Raw("SHOW lock_timeout").Scan(&lockTimeout).Error)
	require.Equal(t, "0", lockTimeout)
}
