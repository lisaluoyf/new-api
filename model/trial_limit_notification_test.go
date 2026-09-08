package model

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTrialLimitNotificationTestDB(t *testing.T) {
	t.Helper()
	oldDB := DB
	oldSQLite := common.UsingSQLite
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}, &SubscriptionPlan{}, &UserSubscription{}, &TrialLimitNotification{}))
	DB = db
	common.UsingSQLite = true
	t.Cleanup(func() {
		DB = oldDB
		common.UsingSQLite = oldSQLite
	})
}

func TestTrialLimitNotificationLifecycleIsIdempotent(t *testing.T) {
	setupTrialLimitNotificationTestDB(t)
	now := time.Now().Unix()
	user := User{Id: 8811, Username: "trial-limit-user", CreatedAt: now}
	plan := SubscriptionPlan{Id: 8812, Title: "APIMaster $50 GPT Trial", PlanType: SubscriptionPlanTypeGPTTrial, Enabled: true}
	subscription := UserSubscription{
		Id: 8813, UserId: user.Id, PlanId: plan.Id, AmountTotal: 1000, AmountUsed: 899,
		Status: "active", StartTime: now, EndTime: now + 86400,
	}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, DB.Create(&plan).Error)
	require.NoError(t, DB.Create(&subscription).Error)

	below, err := CreateTrialLimitNotificationIfReached(user.Id, subscription.Id)
	require.NoError(t, err)
	require.Nil(t, below)
	require.NoError(t, DB.Model(&subscription).Update("amount_used", 900).Error)

	created, err := CreateTrialLimitNotificationIfReached(user.Id, subscription.Id)
	require.NoError(t, err)
	require.NotNil(t, created)
	require.Equal(t, TrialLimitNotificationPending, created.Status)
	require.Len(t, created.ClickToken, 32)

	again, err := CreateTrialLimitNotificationIfReached(user.Id, subscription.Id)
	require.NoError(t, err)
	require.Nil(t, again)
	var count int64
	require.NoError(t, DB.Model(&TrialLimitNotification{}).Count(&count).Error)
	require.EqualValues(t, 1, count)

	require.NoError(t, MarkTrialLimitNotificationSent(created.Id, TrialLimitNotificationTelegram, TrialLimitNotificationStandard, ""))
	clicked, recorded, err := MarkTrialLimitNotificationClicked(created.ClickToken)
	require.NoError(t, err)
	require.True(t, recorded)
	require.Equal(t, TrialLimitNotificationClicked, clicked.Status)
	_, recorded, err = MarkTrialLimitNotificationClicked(created.ClickToken)
	require.NoError(t, err)
	require.False(t, recorded)

	require.NoError(t, MarkTrialLimitNotificationConverted(user.Id, "trial-limit-paid-1"))
	require.NoError(t, MarkTrialLimitNotificationConverted(user.Id, "trial-limit-paid-2"))
	var converted TrialLimitNotification
	require.NoError(t, DB.First(&converted, created.Id).Error)
	require.Equal(t, TrialLimitNotificationConverted, converted.Status)
	require.Equal(t, "trial-limit-paid-1", converted.ConversionTradeNo)
	require.Greater(t, converted.ClickedAt, int64(0))

	metrics, err := GetTrialLimitNotificationDailyMetrics(now-60, now+60)
	require.NoError(t, err)
	require.EqualValues(t, 1, metrics.TriggeredUsers)
	require.EqualValues(t, 1, metrics.Sent)
	require.EqualValues(t, 1, metrics.Clicked)
	require.EqualValues(t, 1, metrics.Converted)
	require.Equal(t, 1.0, metrics.ClickRate)
	require.Equal(t, 1.0, metrics.ConversionRate)
}
