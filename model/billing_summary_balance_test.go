package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGetNonAdminWalletBalanceUSD(t *testing.T) {
	oldDB := DB
	oldQuotaPerUnit := common.QuotaPerUnit
	db, err := gorm.Open(sqlite.Open("file:billing-summary-balance?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}, &BillingWalletDailySnapshot{}))
	DB = db
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() {
		DB = oldDB
		common.QuotaPerUnit = oldQuotaPerUnit
	})

	users := []User{
		{Username: "regular-1", Password: "password", AffCode: "regular-1", Role: common.RoleCommonUser, Quota: 500_000},
		{Username: "regular-2", Password: "password", AffCode: "regular-2", Role: common.RoleCommonUser, Quota: 250_000},
		{Username: "admin", Password: "password", AffCode: "admin", Role: common.RoleAdminUser, Quota: 5_000_000},
		{Username: "root", Password: "password", AffCode: "root", Role: common.RoleRootUser, Quota: 10_000_000},
		{Username: "deleted", Password: "password", AffCode: "deleted", Role: common.RoleCommonUser, Quota: 1_000_000},
	}
	require.NoError(t, db.Create(&users).Error)
	require.NoError(t, db.Delete(&users[4]).Error)

	balanceUSD, err := GetNonAdminWalletBalanceUSD()
	require.NoError(t, err)
	require.Equal(t, 1.5, balanceUSD)

	_, err = UpsertBillingWalletDailySnapshot(100, 110)
	require.NoError(t, err)
	require.NoError(t, db.Model(&User{}).Where("username = ?", "regular-1").Update("quota", 1_000_000).Error)
	latestBalance, err := UpsertBillingWalletDailySnapshot(100, 120)
	require.NoError(t, err)
	require.Equal(t, 2.5, latestBalance)

	snapshots, err := GetBillingWalletDailySnapshots(100, 100)
	require.NoError(t, err)
	require.Equal(t, map[int64]float64{100: 2.5}, snapshots)
	var snapshot BillingWalletDailySnapshot
	require.NoError(t, db.First(&snapshot, "day = ?", 100).Error)
	require.Equal(t, int64(120), snapshot.SnapshotAt)
}
