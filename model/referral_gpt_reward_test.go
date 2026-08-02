package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupReferralGPTRewardTestDB(t *testing.T) {
	t.Helper()
	oldDB := DB
	oldEnabled := common.ReferralGPTRewardEnabled
	oldThreshold := common.ReferralGPTMinTopupUSD
	oldReward := common.ReferralGPTRewardAmountUSD
	oldStartTime := common.ReferralGPTRewardStartTime
	oldQuotaPerUnit := common.QuotaPerUnit

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&Option{},
		&User{},
		&SubscriptionPlan{},
		&UserSubscription{},
		&ReferralGPTRewardLog{},
		&TopUp{},
	))
	DB = db
	common.QuotaPerUnit = 500000
	common.ReferralGPTRewardEnabled = true
	common.ReferralGPTMinTopupUSD = 10
	common.ReferralGPTRewardAmountUSD = 20
	common.ReferralGPTRewardStartTime = 0

	t.Cleanup(func() {
		DB = oldDB
		common.ReferralGPTRewardEnabled = oldEnabled
		common.ReferralGPTMinTopupUSD = oldThreshold
		common.ReferralGPTRewardAmountUSD = oldReward
		common.ReferralGPTRewardStartTime = oldStartTime
		common.QuotaPerUnit = oldQuotaPerUnit
	})
}

func seedSuccessfulReferralTopup(t *testing.T, userId int, tradeNo string, creditedUSD float64, completedAt int64) {
	t.Helper()
	require.NoError(t, DB.Create(&TopUp{
		UserId:          userId,
		CreditedAmount:  creditedUSD,
		TradeNo:         tradeNo,
		PaymentMethod:   PaymentMethodStripe,
		PaymentProvider: PaymentProviderStripe,
		CreateTime:      completedAt,
		CompleteTime:    completedAt,
		Status:          common.TopUpStatusSuccess,
	}).Error)
}

func seedReferralUsers(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.Create(&User{Id: 1, Username: "inviter@example.com", AffCode: "inviter-code"}).Error)
	require.NoError(t, DB.Create(&User{Id: 2, Username: "invitee@example.com", AffCode: "invitee-code", InviterId: 1}).Error)
}

func TestProcessReferralGPTRewardRequiresThreshold(t *testing.T) {
	setupReferralGPTRewardTestDB(t)
	seedReferralUsers(t)

	granted, err := ProcessReferralGPTReward(2, 9*500000, PaymentMethodStripe, "below-threshold", "realtime")
	require.NoError(t, err)
	require.Nil(t, granted)

	var count int64
	require.NoError(t, DB.Model(&ReferralGPTRewardLog{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestProcessReferralGPTRewardAllowsLaterQualifyingTopup(t *testing.T) {
	setupReferralGPTRewardTestDB(t)
	seedReferralUsers(t)

	granted, err := ProcessReferralGPTReward(2, 9*500000, PaymentMethodStripe, "below-threshold-first", "realtime")
	require.NoError(t, err)
	require.Nil(t, granted)

	granted, err = ProcessReferralGPTReward(2, 10*500000, PaymentMethodStripe, "qualifying-second", "realtime")
	require.NoError(t, err)
	require.NotNil(t, granted)
	require.Equal(t, "qualifying-second", granted.TradeNo)
}

func TestProcessReferralGPTRewardUsesCumulativeTopups(t *testing.T) {
	setupReferralGPTRewardTestDB(t)
	seedReferralUsers(t)

	seedSuccessfulReferralTopup(t, 2, "cumulative-one", 1, 100)
	granted, err := ProcessReferralGPTReward(2, 1*500000, PaymentMethodStripe, "cumulative-one", "realtime")
	require.NoError(t, err)
	require.Nil(t, granted)

	seedSuccessfulReferralTopup(t, 2, "cumulative-nine", 9, 200)
	granted, err = ProcessReferralGPTReward(2, 9*500000, PaymentMethodStripe, "cumulative-nine", "realtime")
	require.NoError(t, err)
	require.NotNil(t, granted)
	require.Equal(t, "cumulative-nine", granted.TradeNo)
	require.Equal(t, int64(10*500000), granted.TopupQuota)

	seedSuccessfulReferralTopup(t, 2, "cumulative-later", 20, 300)
	duplicate, err := ProcessReferralGPTReward(2, 20*500000, PaymentMethodStripe, "cumulative-later", "realtime")
	require.NoError(t, err)
	require.Nil(t, duplicate)
}

func TestProcessReferralGPTRewardAttributesThresholdToChronologicalTopup(t *testing.T) {
	setupReferralGPTRewardTestDB(t)
	seedReferralUsers(t)

	seedSuccessfulReferralTopup(t, 2, "out-of-order-one", 1, 100)
	seedSuccessfulReferralTopup(t, 2, "out-of-order-nine", 9, 200)

	firstCallback, err := ProcessReferralGPTReward(2, 1*500000, PaymentMethodStripe, "out-of-order-one", "realtime")
	require.NoError(t, err)
	require.Nil(t, firstCallback)

	secondCallback, err := ProcessReferralGPTReward(2, 9*500000, PaymentMethodStripe, "out-of-order-nine", "realtime")
	require.NoError(t, err)
	require.NotNil(t, secondCallback)
	require.Equal(t, "out-of-order-nine", secondCallback.TradeNo)
	require.Equal(t, int64(10*500000), secondCallback.TopupQuota)
}

func TestProcessReferralGPTRewardGrantsBothSidesOnce(t *testing.T) {
	setupReferralGPTRewardTestDB(t)
	seedReferralUsers(t)

	granted, err := ProcessReferralGPTReward(2, 10*500000, PaymentMethodStripe, "qualified", "realtime")
	require.NoError(t, err)
	require.NotNil(t, granted)
	require.Equal(t, int64(20*500000), granted.InviterRewardQuota)
	require.Equal(t, int64(20*500000), granted.InviteeRewardQuota)

	var plan SubscriptionPlan
	require.NoError(t, DB.Where("plan_type = ?", SubscriptionPlanTypeGPTReferralReward).First(&plan).Error)
	for _, userId := range []int{1, 2} {
		var sub UserSubscription
		require.NoError(t, DB.Where("user_id = ? AND plan_id = ?", userId, plan.Id).First(&sub).Error)
		require.Equal(t, int64(20*500000), sub.AmountTotal)
		require.Zero(t, sub.AmountUsed)
		require.Equal(t, "active", sub.Status)
		require.Equal(t, referralGPTRewardPermanentEndTime, sub.EndTime)
	}

	replayed, err := ProcessReferralGPTReward(2, 10*500000, PaymentMethodStripe, "qualified", "realtime")
	require.NoError(t, err)
	require.Nil(t, replayed)
	later, err := ProcessReferralGPTReward(2, 50*500000, PaymentMethodStripe, "later-topup", "realtime")
	require.NoError(t, err)
	require.Nil(t, later)

	var subscriptions []UserSubscription
	require.NoError(t, DB.Where("plan_id = ?", plan.Id).Order("user_id").Find(&subscriptions).Error)
	require.Len(t, subscriptions, 2)
	for _, sub := range subscriptions {
		require.Equal(t, int64(20*500000), sub.AmountTotal)
	}
}

func TestGetReferralGPTRewardSummaryIncludesBothRoles(t *testing.T) {
	setupReferralGPTRewardTestDB(t)
	seedReferralUsers(t)
	_, err := ProcessReferralGPTReward(2, 10*500000, PaymentMethodStripe, "qualified-summary", "realtime")
	require.NoError(t, err)

	inviterSummary, err := GetReferralGPTRewardSummary(1)
	require.NoError(t, err)
	require.Equal(t, int64(20*500000), inviterSummary.RemainingQuota)
	require.Equal(t, int64(20*500000), inviterSummary.CumulativeQuota)
	require.Equal(t, int64(1), inviterSummary.QualifiedInvitees)
	require.Equal(t, 20.0, inviterSummary.RewardUSD)

	inviteeSummary, err := GetReferralGPTRewardSummary(2)
	require.NoError(t, err)
	require.Equal(t, int64(20*500000), inviteeSummary.RemainingQuota)
	require.Equal(t, int64(20*500000), inviteeSummary.CumulativeQuota)
	require.Zero(t, inviteeSummary.QualifiedInvitees)
}

func TestReferralGPTRewardsStackInSingleInviterSubscription(t *testing.T) {
	setupReferralGPTRewardTestDB(t)
	seedReferralUsers(t)
	require.NoError(t, DB.Create(&User{Id: 3, Username: "invitee-two@example.com", AffCode: "invitee-two-code", InviterId: 1}).Error)

	first, err := ProcessReferralGPTReward(2, 10*500000, PaymentMethodStripe, "first-invitee", "realtime")
	require.NoError(t, err)
	require.NotNil(t, first)
	second, err := ProcessReferralGPTReward(3, 10*500000, PaymentMethodStripe, "second-invitee", "realtime")
	require.NoError(t, err)
	require.NotNil(t, second)

	var plan SubscriptionPlan
	require.NoError(t, DB.Where("plan_type = ?", SubscriptionPlanTypeGPTReferralReward).First(&plan).Error)
	var inviterSubscriptions []UserSubscription
	require.NoError(t, DB.Where("user_id = ? AND plan_id = ?", 1, plan.Id).Find(&inviterSubscriptions).Error)
	require.Len(t, inviterSubscriptions, 1)
	require.Equal(t, int64(40*500000), inviterSubscriptions[0].AmountTotal)
}
