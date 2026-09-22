package service

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestBillingHoldRefundWithDeletedToken(t *testing.T) {
	for _, funding := range []string{"wallet", "subscription"} {
		for _, credential := range []string{"active", "deleted", "missing", "none"} {
			t.Run(funding+"/"+credential, func(t *testing.T) {
				truncate(t)
				user := model.User{Id: 201, Username: "hold-refund-user", Quota: 1000}
				require.NoError(t, model.DB.Create(&user).Error)
				token := model.Token{Id: 201, UserId: user.Id, Key: "hold-refund-test-key", RemainQuota: 700, UsedQuota: 300}
				tokenID := token.Id
				if credential == "none" {
					tokenID = 0
				} else {
					require.NoError(t, model.DB.Create(&token).Error)
					if credential == "deleted" {
						require.NoError(t, model.DB.Delete(&token).Error)
					}
					if credential == "missing" {
						require.NoError(t, model.DB.Unscoped().Delete(&token).Error)
					}
				}
				now := common.GetTimestamp()
				hold := model.BillingHold{RequestId: "deleted-token-refund", UserId: user.Id, TokenId: tokenID,
					PreConsumedQuota: 100, Status: model.BillingHoldStatusPending, CreatedAt: now - 3600, ReconcileAfter: now - 10,
					ErrorStatus: 400, ErrorCode: "moderation_blocked", ErrorMessage: "rejected by safety system"}
				require.NoError(t, model.CreateBillingHold(&hold))
				if funding == "subscription" {
					require.NoError(t, model.DB.Create(&model.UserSubscription{Id: 201, UserId: user.Id, AmountTotal: 1000, AmountUsed: 400}).Error)
					require.NoError(t, model.DB.Create(&model.SubscriptionPreConsumeRecord{RequestId: hold.RequestId, UserId: user.Id,
						UserSubscriptionId: 201, PreConsumed: 100, Status: "consumed"}).Error)
				}
				runBillingHoldReconcile(hold.Id)
				runBillingHoldReconcile(hold.Id) // Repeated scheduler delivery must not refund again.
				updated, err := model.GetBillingHoldById(hold.Id)
				require.NoError(t, err)
				require.Equal(t, model.BillingHoldStatusRefunded, updated.Status)
				require.NoError(t, model.DB.First(&user, user.Id).Error)
				if funding == "subscription" {
					require.Equal(t, 1000, user.Quota)
					var subscription model.UserSubscription
					require.NoError(t, model.DB.First(&subscription, 201).Error)
					require.EqualValues(t, 300, subscription.AmountUsed)
				} else {
					require.Equal(t, 1100, user.Quota)
				}
				if credential == "active" || credential == "deleted" {
					var actual model.Token
					require.NoError(t, model.DB.Unscoped().First(&actual, token.Id).Error)
					require.Equal(t, 800, actual.RemainQuota)
					require.Equal(t, 200, actual.UsedQuota)
					require.Equal(t, credential == "deleted", actual.DeletedAt.Valid)
				} else if credential == "missing" {
					require.Contains(t, updated.VerifyDetail, "token missing")
					var count int64
					require.NoError(t, model.DB.Unscoped().Model(&model.Token{}).Where("id = ?", token.Id).Count(&count).Error)
					require.Zero(t, count)
				}
				var logs int64
				require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("request_id = ? AND type = ?", hold.RequestId, model.LogTypeRefund).Count(&logs).Error)
				require.EqualValues(t, 1, logs)
			})
		}
	}
}

func TestBillingHoldFailuresDoNotStarveFollowingRows(t *testing.T) {
	truncate(t)
	now := common.GetTimestamp()
	// Missing users are a genuine failure; their funds cannot be reassigned.
	for i := 0; i < 200; i++ {
		hold := model.BillingHold{RequestId: fmt.Sprintf("failing-%03d", i), UserId: 999, PreConsumedQuota: 100,
			Status: model.BillingHoldStatusPending, CreatedAt: now - 3600, ReconcileAfter: now - 1000,
			ErrorStatus: 400, ErrorCode: "moderation_blocked"}
		require.NoError(t, model.CreateBillingHold(&hold))
	}
	require.NoError(t, model.DB.Create(&model.User{Id: 202, Username: "following-user", Quota: 1000}).Error)
	healthy := model.BillingHold{RequestId: "following-healthy", UserId: 202, PreConsumedQuota: 100,
		Status: model.BillingHoldStatusPending, CreatedAt: now - 3600, ReconcileAfter: now - 500,
		ErrorStatus: 400, ErrorCode: "moderation_blocked"}
	require.NoError(t, model.CreateBillingHold(&healthy))
	first, err := model.ListDueBillingHolds(now, 200)
	require.NoError(t, err)
	require.Len(t, first, 200)
	for _, hold := range first {
		runBillingHoldReconcile(hold.Id)
	}
	next, err := model.ListDueBillingHolds(common.GetTimestamp(), 200)
	require.NoError(t, err)
	require.Len(t, next, 1)
	require.Equal(t, healthy.Id, next[0].Id)
	runBillingHoldReconcile(healthy.Id)
	updated, err := model.GetBillingHoldById(healthy.Id)
	require.NoError(t, err)
	require.Equal(t, model.BillingHoldStatusRefunded, updated.Status)
	failed, err := model.GetBillingHoldById(first[0].Id)
	require.NoError(t, err)
	require.GreaterOrEqual(t, failed.ReconcileAfter, now+300)
	require.Contains(t, failed.VerifyDetail, "reconcile failed")
	claimed, err := model.ClaimBillingHold(failed.Id)
	require.NoError(t, err)
	require.False(t, claimed, "a scheduled goroutine cannot bypass durable retry time")
}
