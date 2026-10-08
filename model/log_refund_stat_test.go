package model

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSumUsedQuotaExcludesRefundedConsumptionWithoutHidingRequests(t *testing.T) {
	oldLogDB := LOG_DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	LOG_DB = db
	t.Cleanup(func() { LOG_DB = oldLogDB })
	require.NoError(t, db.AutoMigrate(&Log{}))
	now := time.Now().Unix()
	rows := []Log{
		{UserId: 42, Type: LogTypeConsume, CreatedAt: now, Quota: 100, PromptTokens: 10},
		{UserId: 42, Type: LogTypeConsume, CreatedAt: now, Quota: 200, PromptTokens: 20, AccountingStatus: AccountingStatusRefunded},
		{UserId: 42, Type: LogTypeRefund, CreatedAt: now, Quota: 200},
		{UserId: 43, Type: LogTypeConsume, CreatedAt: now, Quota: 300},
	}
	require.NoError(t, db.Create(&rows).Error)
	stat, err := SumUsedQuota(LogTypeUnknown, now-1, now+1, "", "", "", 0, "", 42)
	require.NoError(t, err)
	require.EqualValues(t, 100, stat.Quota)
	require.EqualValues(t, 2, stat.Rpm)
	require.EqualValues(t, 30, stat.Tpm)
	require.NoError(t, db.Where("user_id = ? AND type = ?", 42, LogTypeConsume).
		Model(&Log{}).Update("accounting_status", AccountingStatusRefunded).Error)
	stat, err = SumUsedQuota(LogTypeUnknown, now-1, now+1, "", "", "", 0, "", 42)
	require.NoError(t, err)
	require.Zero(t, stat.Quota)
	var original Log
	require.NoError(t, db.First(&original, rows[1].Id).Error)
	require.Equal(t, 200, original.Quota)
}
