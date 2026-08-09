package model

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupBillingHoldSubscriptionTestDB(t *testing.T) {
	t.Helper()
	oldDB := DB
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{}, &BillingHold{}))
	DB = db
	t.Cleanup(func() { DB = oldDB })
}

func TestResolveBillingHoldRefundRestoresSubscription(t *testing.T) {
	setupBillingHoldSubscriptionTestDB(t)
	require.NoError(t, DB.Create(&User{Id: 1, Username: "subscription-user", Quota: 100}).Error)
	require.NoError(t, DB.Create(&UserSubscription{Id: 10, UserId: 1, AmountTotal: 1000, AmountUsed: 400, Status: "active"}).Error)
	require.NoError(t, DB.Create(&SubscriptionPreConsumeRecord{
		RequestId:          "subscription-refund-request",
		UserId:             1,
		UserSubscriptionId: 10,
		PreConsumed:        100,
		Status:             "consumed",
	}).Error)
	hold := &BillingHold{
		RequestId:        "subscription-refund-request",
		UserId:           1,
		PreConsumedQuota: 100,
		Status:           "processing",
	}
	require.NoError(t, DB.Create(hold).Error)

	require.NoError(t, ResolveBillingHoldRefund(hold, false, "not charged", ""))

	var user User
	var sub UserSubscription
	var record SubscriptionPreConsumeRecord
	var updatedHold BillingHold
	require.NoError(t, DB.First(&user, 1).Error)
	require.NoError(t, DB.First(&sub, 10).Error)
	require.NoError(t, DB.First(&record, "request_id = ?", "subscription-refund-request").Error)
	require.NoError(t, DB.First(&updatedHold, hold.Id).Error)
	require.Equal(t, 100, user.Quota, "subscription refund must not credit wallet")
	require.Equal(t, int64(300), sub.AmountUsed)
	require.Equal(t, "refunded", record.Status)
	require.Equal(t, BillingHoldStatusRefunded, updatedHold.Status)
}

func TestResolveBillingHoldConfirmDoesNotPolluteWalletForSubscription(t *testing.T) {
	setupBillingHoldSubscriptionTestDB(t)
	require.NoError(t, DB.Create(&User{Id: 1, Username: "subscription-user", Quota: 100, UsedQuota: 25}).Error)
	require.NoError(t, DB.Create(&UserSubscription{Id: 10, UserId: 1, AmountTotal: 1000, AmountUsed: 400, Status: "active"}).Error)
	require.NoError(t, DB.Create(&SubscriptionPreConsumeRecord{
		RequestId:          "subscription-confirm-request",
		UserId:             1,
		UserSubscriptionId: 10,
		PreConsumed:        100,
		Status:             "consumed",
	}).Error)
	hold := &BillingHold{
		RequestId:        "subscription-confirm-request",
		UserId:           1,
		PreConsumedQuota: 100,
		Status:           "processing",
	}
	require.NoError(t, DB.Create(hold).Error)

	require.NoError(t, ResolveBillingHoldConfirm(hold, false, "charged"))

	var user User
	var sub UserSubscription
	var updatedHold BillingHold
	require.NoError(t, DB.First(&user, 1).Error)
	require.NoError(t, DB.First(&sub, 10).Error)
	require.NoError(t, DB.First(&updatedHold, hold.Id).Error)
	require.Equal(t, 25, user.UsedQuota, "subscription confirmation must not update wallet used_quota")
	require.Equal(t, int64(400), sub.AmountUsed, "pre-consume already accounts for the subscription usage")
	require.Equal(t, BillingHoldStatusConfirmed, updatedHold.Status)
}
