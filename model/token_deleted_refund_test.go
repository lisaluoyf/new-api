package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRefundUpdatesDeletedTokenCountersWithoutRestoringAccess(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	previousDB, previousRedis, previousBatch := DB, common.RedisEnabled, common.BatchUpdateEnabled
	DB, common.RedisEnabled, common.BatchUpdateEnabled = db, false, false
	t.Cleanup(func() {
		DB, common.RedisEnabled, common.BatchUpdateEnabled = previousDB, previousRedis, previousBatch
		_ = sqlDB.Close()
	})
	require.NoError(t, db.AutoMigrate(&Token{}))
	token := Token{UserId: 1, Key: "test-deleted-refund-token", UsedQuota: 80, RemainQuota: 20}
	require.NoError(t, db.Create(&token).Error)
	require.NoError(t, db.Delete(&token).Error)
	require.NoError(t, IncreaseTokenQuota(token.Id, token.Key, 50))
	var stored Token
	require.NoError(t, db.Unscoped().First(&stored, token.Id).Error)
	require.Equal(t, 30, stored.UsedQuota)
	require.Equal(t, 70, stored.RemainQuota)
	require.True(t, stored.DeletedAt.Valid)
	var visible int64
	require.NoError(t, db.Model(&Token{}).Where("id = ?", token.Id).Count(&visible).Error)
	require.Zero(t, visible)
	// The batch updater uses the same function, including debit adjustments.
	require.NoError(t, increaseTokenQuota(token.Id, -5))
	require.NoError(t, db.Unscoped().First(&stored, token.Id).Error)
	require.Equal(t, 35, stored.UsedQuota)
	require.Equal(t, 65, stored.RemainQuota)
	require.True(t, stored.DeletedAt.Valid)
}
