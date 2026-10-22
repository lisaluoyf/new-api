package model

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupCallCostTestDB(t *testing.T) {
	t.Helper()
	old := LOG_DB
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "cost.db")), &gorm.Config{})
	require.NoError(t, err)
	LOG_DB = db
	t.Cleanup(func() { LOG_DB = old; sqlDB, _ := db.DB(); sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&Log{}, &CallCostAlertState{}))
}

func TestCallCostTotalsUsesProcurementNotRevenue(t *testing.T) {
	setupCallCostTestDB(t)
	rows := []Log{
		{Id: 1, ChannelId: 59, Type: LogTypeConsume, CreatedAt: 100, AccountingChannelCostAmountUSD: 0.1, AccountingStatus: "ok", AccountingUserFinalAmountUSD: 999},
		{Id: 2, ChannelId: 59, Type: LogTypeConsume, CreatedAt: 120, AccountingChannelCostAmountUSD: 0.2, AccountingStatus: "partial"},
		{Id: 3, ChannelId: 59, Type: LogTypeConsume, CreatedAt: 130, AccountingStatus: "partial"},
		{Id: 4, ChannelId: 59, Type: LogTypeConsume, CreatedAt: 140, AccountingChannelCostAmountUSD: 800, AccountingStatus: AccountingStatusRefunded},
		{Id: 5, ChannelId: 60, Type: LogTypeConsume, CreatedAt: 150, AccountingChannelCostAmountUSD: 2, AccountingStatus: "ok"},
		{Id: 6, ChannelId: 60, Type: LogTypeConsume, CreatedAt: 160, AccountingChannelCostAmountUSD: 900, AccountingStatus: "error"},
		{Id: 7, ChannelId: 59, Type: LogTypeRefund, CreatedAt: 170, AccountingChannelCostAmountUSD: 50, AccountingStatus: "ok"},
		{Id: 8, ChannelId: 59, Type: LogTypeError, CreatedAt: 180, AccountingChannelCostAmountUSD: 60, AccountingStatus: "ok"},
		{Id: 9, ChannelId: 59, Type: LogTypeConsume, CreatedAt: 200, AccountingChannelCostAmountUSD: 70, AccountingStatus: "ok"},
		{Id: 10, ChannelId: 59, Type: LogTypeConsume, CreatedAt: 99, AccountingChannelCostAmountUSD: 80, AccountingStatus: "ok"},
		{Id: 11, ChannelId: 60, Type: LogTypeConsume, CreatedAt: 170, AccountingChannelCostAmountUSD: -1, AccountingStatus: "ok"},
		{Id: 12, ChannelId: 60, Type: LogTypeConsume, CreatedAt: 180, AccountingChannelCostAmountUSD: 900},
	}
	require.NoError(t, LOG_DB.Create(&rows).Error)
	ctx := context.Background()
	totals, err := GetCallCostTotals(ctx, 100, 200, nil)
	require.NoError(t, err)
	require.Len(t, totals, 2)
	require.EqualValues(t, 3, totals[0].RequestCount)
	require.EqualValues(t, 1, totals[0].UnknownRequests)
	require.True(t, totals[0].AmountUSD().Equal(decimal.RequireFromString("0.3")))
	require.EqualValues(t, 4, totals[1].RequestCount)
	require.EqualValues(t, 3, totals[1].UnknownRequests)
	require.True(t, totals[1].AmountUSD().Equal(decimal.NewFromInt(2)))
	filtered, err := GetCallCostTotals(ctx, 100, 200, []int{60})
	require.NoError(t, err)
	require.Equal(t, totals[1:], filtered)
	// Task correction replaces the amount on the original row, not another cost.
	require.NoError(t, LOG_DB.Model(&Log{}).Where("id = ?", 2).Update("accounting_channel_cost_amount_usd", 0.4).Error)
	corrected, err := GetCallCostTotals(ctx, 100, 200, []int{59})
	require.NoError(t, err)
	require.True(t, corrected[0].AmountUSD().Equal(decimal.RequireFromString("0.5")))
	_, err = GetCallCostTotals(ctx, 200, 100, nil)
	require.Error(t, err)
	empty, err := GetCallCostTotals(ctx, 300, 400, nil)
	require.NoError(t, err)
	require.Empty(t, empty)
}

func TestCallCostAlertPersistentLeaseRetryAndCooldown(t *testing.T) {
	setupCallCostTestDB(t)
	require.NoError(t, LOG_DB.AutoMigrate(&CallCostAlertState{}))
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	token, acquired, err := AcquireCallCostAlert(ctx, "daily-rule", now)
	require.NoError(t, err)
	require.True(t, acquired)
	_, acquired, err = AcquireCallCostAlert(ctx, "daily-rule", now)
	require.NoError(t, err)
	require.False(t, acquired)                                           // another instance
	require.NoError(t, FinishCallCostAlert(ctx, "daily-rule", token, 0)) // send failed
	token, acquired, err = AcquireCallCostAlert(ctx, "daily-rule", now.Add(time.Minute))
	require.NoError(t, err)
	require.True(t, acquired)
	require.NoError(t, FinishCallCostAlert(ctx, "daily-rule", token, now.Add(time.Hour).Unix()))
	_, acquired, err = AcquireCallCostAlert(ctx, "daily-rule", now.Add(2*time.Minute))
	require.NoError(t, err)
	require.False(t, acquired) // no in-process state; restart cannot erase cooldown
	_, acquired, err = AcquireCallCostAlert(ctx, "daily-rule", now.Add(time.Hour))
	require.NoError(t, err)
	require.True(t, acquired)

	old, acquired, err := AcquireCallCostAlert(ctx, "crashed", now)
	require.NoError(t, err)
	require.True(t, acquired)
	newToken, acquired, err := AcquireCallCostAlert(ctx, "crashed", now.Add(2*time.Minute))
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotEqual(t, old, newToken)
	require.Error(t, FinishCallCostAlert(ctx, "crashed", old, now.Add(time.Hour).Unix()))
	require.NoError(t, FinishCallCostAlert(ctx, "crashed", newToken, 0))
}

func TestCallCostStoreMigrationFailureLeavesExistingDatabaseUsable(t *testing.T) {
	setupCallCostTestDB(t)
	require.NoError(t, LOG_DB.Migrator().DropTable(&CallCostAlertState{}))
	sqlDB, err := LOG_DB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, LOG_DB.Exec("PRAGMA query_only = ON").Error)
	require.Error(t, InitCallCostAlertStore(context.Background()))
	var count int64
	require.NoError(t, LOG_DB.Model(&Log{}).Count(&count).Error)
	require.False(t, LOG_DB.Migrator().HasTable(&CallCostAlertState{}))
	require.NoError(t, LOG_DB.Exec("PRAGMA query_only = OFF").Error)
	require.NoError(t, InitCallCostAlertStore(context.Background()))
	require.True(t, LOG_DB.Migrator().HasTable(&CallCostAlertState{}))
}

func TestCallCostStoreHonorsInitializationDeadline(t *testing.T) {
	setupCallCostTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Error(t, InitCallCostAlertStore(ctx))
	var count int64
	require.NoError(t, LOG_DB.Model(&Log{}).Count(&count).Error)
}
