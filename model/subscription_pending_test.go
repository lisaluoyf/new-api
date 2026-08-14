package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnrichSubscriptionPendingAmounts(t *testing.T) {
	setupBillingHoldSubscriptionTestDB(t)

	subs := []UserSubscription{
		{Id: 10, UserId: 1},
		{Id: 20, UserId: 1},
	}
	for _, record := range []SubscriptionPreConsumeRecord{
		{RequestId: "pending-1", UserId: 1, UserSubscriptionId: 10, PreConsumed: 100, Status: "consumed"},
		{RequestId: "processing-1", UserId: 1, UserSubscriptionId: 10, PreConsumed: 200, Status: "consumed"},
		{RequestId: "confirmed-1", UserId: 1, UserSubscriptionId: 10, PreConsumed: 300, Status: "consumed"},
		{RequestId: "pending-2", UserId: 1, UserSubscriptionId: 20, PreConsumed: 400, Status: "consumed"},
	} {
		require.NoError(t, DB.Create(&record).Error)
	}
	for _, hold := range []BillingHold{
		{RequestId: "pending-1", UserId: 1, PreConsumedQuota: 100, Status: BillingHoldStatusPending},
		{RequestId: "processing-1", UserId: 1, PreConsumedQuota: 200, Status: "processing"},
		{RequestId: "confirmed-1", UserId: 1, PreConsumedQuota: 300, Status: BillingHoldStatusConfirmed},
		{RequestId: "pending-2", UserId: 1, PreConsumedQuota: 400, Status: BillingHoldStatusPending},
	} {
		require.NoError(t, DB.Create(&hold).Error)
	}

	require.NoError(t, enrichSubscriptionPendingAmounts(subs))
	require.EqualValues(t, 300, subs[0].PendingAmount)
	require.EqualValues(t, 400, subs[1].PendingAmount)
}
