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
	require.NoError(t, db.AutoMigrate(&User{}, &UserSubscription{}, &BillingWalletDailySnapshot{}, &BillingSubscriptionDailySnapshot{}, &BillingExperienceDailySnapshot{}, &BillingPaidSubscriptionDailySnapshot{}, &BillingCodingPlanDailySnapshot{}, &SubscriptionPlan{}))
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

func TestGetNonAdminSubscriptionBalanceUSD(t *testing.T) {
	oldDB := DB
	oldQuotaPerUnit := common.QuotaPerUnit
	db, err := gorm.Open(sqlite.Open("file:billing-summary-subscription-balance?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}, &UserSubscription{}, &BillingSubscriptionDailySnapshot{}, &BillingExperienceDailySnapshot{}, &BillingPaidSubscriptionDailySnapshot{}, &BillingCodingPlanDailySnapshot{}, &SubscriptionPlan{}))
	DB = db
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() {
		DB = oldDB
		common.QuotaPerUnit = oldQuotaPerUnit
	})

	users := []User{
		{Id: 1, Username: "regular-1", Password: "password", AffCode: "regular-1", Role: common.RoleCommonUser},
		{Id: 2, Username: "regular-2", Password: "password", AffCode: "regular-2", Role: common.RoleCommonUser},
		{Id: 3, Username: "admin", Password: "password", AffCode: "admin", Role: common.RoleAdminUser},
		{Id: 4, Username: "deleted", Password: "password", AffCode: "deleted", Role: common.RoleCommonUser},
	}
	require.NoError(t, db.Create(&users).Error)
	require.NoError(t, db.Delete(&users[3]).Error)

	plans := []SubscriptionPlan{
		{Id: 101, Title: "APIMaster $20 GPT Trial", PlanType: SubscriptionPlanTypeGPTTrial, Currency: "USD", DurationUnit: SubscriptionDurationDay, DurationValue: 5},
		{Id: 102, Title: "Referral Reward", PlanType: SubscriptionPlanTypeGPTReferralReward, Currency: "USD", DurationUnit: SubscriptionDurationDay, DurationValue: 365},
		{Id: 103, Title: "GPT Subscription A", PlanType: SubscriptionPlanTypeGPTSubscription, Currency: "USD", DurationUnit: SubscriptionDurationMonth, DurationValue: 1, PriceAmount: 100},
		{Id: 104, Title: "GPT Subscription B", PlanType: SubscriptionPlanTypeGPTSubscription, Currency: "USD", DurationUnit: SubscriptionDurationMonth, DurationValue: 1, PriceAmount: 40},
		{Id: 105, Title: "Coding Plan", PlanType: SubscriptionPlanTypeCodingPlan, Currency: "USD", DurationUnit: SubscriptionDurationMonth, DurationValue: 1, PriceAmount: 60},
	}
	require.NoError(t, db.Create(&plans).Error)

	now := common.GetTimestamp()
	subs := []UserSubscription{
		{UserId: 1, PlanId: 101, AmountTotal: 1_500_000, AmountUsed: 500_000, Status: "active", EndTime: now + 3600},
		{UserId: 2, PlanId: 102, AmountTotal: 800_000, AmountUsed: 300_000, Status: "active", EndTime: now + 7200},
		{UserId: 2, PlanId: 103, AmountTotal: 1_200_000, AmountUsed: 200_000, Status: "active", StartTime: now - 15*86400, EndTime: now + 15*86400, PriceAmountSnapshot: 100, DurationSecondsSnapshot: 30 * 86400},
		{UserId: 1, PlanId: 104, AmountTotal: 900_000, AmountUsed: 100_000, Status: "active", StartTime: now - 15*86400, EndTime: now + 15*86400, PriceAmountSnapshot: 0, DurationSecondsSnapshot: 0},
		{UserId: 1, PlanId: 105, AmountTotal: 1_000_000, AmountUsed: 250_000, Status: "active", StartTime: now - 15*86400, EndTime: now + 15*86400, PriceAmountSnapshot: 60, DurationSecondsSnapshot: 30 * 86400},
		{UserId: 2, PlanId: 101, AmountTotal: 100_000, AmountUsed: 200_000, Status: "active", EndTime: now + 7200}, // clamped to 0
		{UserId: 3, PlanId: 103, AmountTotal: 5_000_000, AmountUsed: 0, Status: "active", EndTime: now + 3600},     // admin excluded
		{UserId: 4, PlanId: 101, AmountTotal: 2_000_000, AmountUsed: 0, Status: "active", EndTime: now + 3600},     // deleted excluded
		{UserId: 1, PlanId: 101, AmountTotal: 900_000, AmountUsed: 0, Status: "expired", EndTime: now + 3600},      // status excluded
		{UserId: 1, PlanId: 101, AmountTotal: 900_000, AmountUsed: 0, Status: "active", EndTime: now - 10},         // expired by time excluded
	}
	require.NoError(t, db.Create(&subs).Error)

	balanceUSD, err := GetNonAdminSubscriptionBalanceUSD()
	require.NoError(t, err)
	require.Equal(t, 8.1, balanceUSD)

	experienceBalanceUSD, err := GetNonAdminExperienceBalanceUSD()
	require.NoError(t, err)
	require.Equal(t, 3.0, experienceBalanceUSD)

	paidSubscriptionBalanceUSD, err := GetNonAdminPaidSubscriptionBalanceUSD()
	require.NoError(t, err)
	require.Equal(t, 70.0, paidSubscriptionBalanceUSD)

	paidSubscriptionBalanceAtNow, err := GetNonAdminPaidSubscriptionBalanceUSDAt(now)
	require.NoError(t, err)
	require.Equal(t, 70.0, paidSubscriptionBalanceAtNow)

	codingPlanBalanceUSD, err := GetNonAdminCodingPlanBalanceUSD()
	require.NoError(t, err)
	require.Equal(t, 45.0, codingPlanBalanceUSD)

	codingPlanBalanceAtNow, err := GetNonAdminCodingPlanBalanceUSDAt(now)
	require.NoError(t, err)
	require.Equal(t, 45.0, codingPlanBalanceAtNow)

	_, err = UpsertBillingSubscriptionDailySnapshot(100, 110)
	require.NoError(t, err)
	_, err = UpsertBillingExperienceDailySnapshot(100, 110)
	require.NoError(t, err)
	_, err = UpsertBillingPaidSubscriptionDailySnapshot(100, now)
	require.NoError(t, err)
	_, err = UpsertBillingCodingPlanDailySnapshot(100, now)
	require.NoError(t, err)
	require.NoError(t, db.Model(&UserSubscription{}).Where("user_id = ? AND plan_id = ?", 1, 101).Update("amount_used", 0).Error)
	latestBalance, err := UpsertBillingSubscriptionDailySnapshot(100, 120)
	require.NoError(t, err)
	require.Equal(t, 9.1, latestBalance)
	latestExperienceBalance, err := UpsertBillingExperienceDailySnapshot(100, 120)
	require.NoError(t, err)
	require.Equal(t, 4.0, latestExperienceBalance)
	require.NoError(t, db.Model(&UserSubscription{}).Where("user_id = ? AND plan_id = ?", 2, 103).Update("amount_used", 999_999).Error)
	latestPaidBalance, err := UpsertBillingPaidSubscriptionDailySnapshot(100, now)
	require.NoError(t, err)
	require.Equal(t, 70.0, latestPaidBalance)
	latestCodingBalance, err := UpsertBillingCodingPlanDailySnapshot(100, now)
	require.NoError(t, err)
	require.Equal(t, 45.0, latestCodingBalance)

	snapshots, err := GetBillingSubscriptionDailySnapshots(100, 100)
	require.NoError(t, err)
	require.Equal(t, map[int64]float64{100: 9.1}, snapshots)
	experienceSnapshots, err := GetBillingExperienceDailySnapshots(100, 100)
	require.NoError(t, err)
	require.Equal(t, map[int64]float64{100: 4.0}, experienceSnapshots)
	paidSnapshots, err := GetBillingPaidSubscriptionDailySnapshots(100, 100)
	require.NoError(t, err)
	require.Equal(t, map[int64]float64{100: 70.0}, paidSnapshots)
	codingSnapshots, err := GetBillingCodingPlanDailySnapshots(100, 100)
	require.NoError(t, err)
	require.Equal(t, map[int64]float64{100: 45.0}, codingSnapshots)
	var snapshot BillingSubscriptionDailySnapshot
	require.NoError(t, db.First(&snapshot, "day = ?", 100).Error)
	require.Equal(t, int64(120), snapshot.SnapshotAt)
	var paidSnapshot BillingPaidSubscriptionDailySnapshot
	require.NoError(t, db.First(&paidSnapshot, "day = ?", 100).Error)
	require.Equal(t, now, paidSnapshot.SnapshotAt)
	var codingSnapshot BillingCodingPlanDailySnapshot
	require.NoError(t, db.First(&codingSnapshot, "day = ?", 100).Error)
	require.Equal(t, now, codingSnapshot.SnapshotAt)
}

func TestGetBillingPaidSubscriptionDailyAccruals(t *testing.T) {
	oldDB := DB
	db, err := gorm.Open(sqlite.Open("file:billing-summary-paid-accruals?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}, &UserSubscription{}, &SubscriptionPlan{}))
	DB = db
	t.Cleanup(func() {
		DB = oldDB
	})

	users := []User{
		{Id: 11, Username: "paid-1", AffCode: "paid-1", Role: common.RoleCommonUser},
		{Id: 12, Username: "paid-2", AffCode: "paid-2", Role: common.RoleCommonUser},
		{Id: 13, Username: "cancelled", AffCode: "cancelled", Role: common.RoleCommonUser},
	}
	require.NoError(t, db.Create(&users).Error)

	plan := SubscriptionPlan{
		Id:            201,
		Title:         "GPT Subscription",
		PlanType:      SubscriptionPlanTypeGPTSubscription,
		PriceAmount:   48,
		Currency:      "USD",
		DurationUnit:  SubscriptionDurationDay,
		DurationValue: 2,
	}
	require.NoError(t, db.Create(&plan).Error)

	dayStart := billingDayStartUnix(common.GetTimestamp())
	subs := []UserSubscription{
		{UserId: 11, PlanId: 201, Status: "active", StartTime: dayStart, EndTime: dayStart + 2*86400, PriceAmountSnapshot: 48, DurationSecondsSnapshot: 2 * 86400},
		{UserId: 12, PlanId: 201, Status: "active", StartTime: dayStart + 12*3600, EndTime: dayStart + 36*3600, PriceAmountSnapshot: 24, DurationSecondsSnapshot: 24 * 3600},
		{UserId: 13, PlanId: 201, Status: "cancelled", StartTime: dayStart, EndTime: dayStart + 86400, PriceAmountSnapshot: 24, DurationSecondsSnapshot: 86400},
	}
	require.NoError(t, db.Create(&subs).Error)

	accruals, err := GetBillingPaidSubscriptionDailyAccruals(dayStart, dayStart+2*86400-1)
	require.NoError(t, err)
	require.Len(t, accruals, 2)
	require.InDelta(t, 36, accruals[dayStart].RevenueUSD, 1e-9)
	require.Equal(t, int64(2), accruals[dayStart].UserCount)
	require.InDelta(t, 36, accruals[dayStart+86400].RevenueUSD, 1e-9)
	require.Equal(t, int64(2), accruals[dayStart+86400].UserCount)

	totalUsers, err := GetBillingPaidSubscriptionUserCountTotal(dayStart, dayStart+2*86400-1)
	require.NoError(t, err)
	require.Equal(t, int64(2), totalUsers)
}
