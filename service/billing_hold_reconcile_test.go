package service

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifyBillingHoldUpstreamCharge_confirmedNotError(t *testing.T) {
	hold := &model.BillingHold{
		ErrorStatus:  400,
		ErrorCode:    string(types.ErrorCodeConvertRequestFailed),
		ErrorMessage: "convert failed",
	}
	decision, detail := VerifyBillingHoldUpstreamCharge(hold)
	require.Equal(t, BillingHoldDecisionRefund, decision, detail)
}

func TestVerifyBillingHoldUpstreamCharge_receivedResponses(t *testing.T) {
	hold := &model.BillingHold{
		ErrorStatus:       504,
		ErrorCode:         string(types.ErrorCodeBadResponseStatusCode),
		ErrorMessage:      "gateway timeout",
		ReceivedResponses: 12,
	}
	decision, detail := VerifyBillingHoldUpstreamCharge(hold)
	require.Equal(t, BillingHoldDecisionUnknown, decision, detail)
}

func TestVerifyBillingHoldUpstreamCharge_ambiguousStaysUnknown(t *testing.T) {
	hold := &model.BillingHold{
		ErrorStatus:  502,
		ErrorCode:    string(types.ErrorCodeBadResponseStatusCode),
		ErrorMessage: "bad gateway",
	}
	decision, detail := VerifyBillingHoldUpstreamCharge(hold)
	require.Equal(t, BillingHoldDecisionUnknown, decision)
	if detail == "" {
		t.Fatal("expected detail")
	}
}

func TestVerifyBillingHoldUpstreamCharge_moderationErrorRefunds(t *testing.T) {
	hold := &model.BillingHold{
		ErrorStatus:  502,
		ErrorCode:    "moderation_blocked",
		ErrorMessage: "Your request was rejected by the safety system",
	}
	decision, detail := VerifyBillingHoldUpstreamCharge(hold)
	require.Equal(t, BillingHoldDecisionRefund, decision, detail)
}

func TestBillingHoldUnknownExpired(t *testing.T) {
	hold := &model.BillingHold{CreatedAt: 100}
	require.False(t, billingHoldUnknownExpired(hold, 100+billingHoldSyncUnknownMaxAgeSec-1))
	require.True(t, billingHoldUnknownExpired(hold, 100+billingHoldSyncUnknownMaxAgeSec))

	asyncHold := &model.BillingHold{CreatedAt: 100, UpstreamTaskId: "task-1"}
	require.False(t, billingHoldUnknownExpired(asyncHold, 100+billingHoldAsyncUnknownMaxAgeSec-1))
	require.True(t, billingHoldUnknownExpired(asyncHold, 100+billingHoldAsyncUnknownMaxAgeSec))
}

func TestBillingHoldReconcilePolicy(t *testing.T) {
	syncHold := &model.BillingHold{}
	assert.Equal(t, int64(billingHoldSyncReconcileDelaySec), billingHoldReconcileDelaySecFor(syncHold))
	assert.Equal(t, int64(billingHoldSyncUnknownMaxAgeSec), billingHoldUnknownMaxAgeFor(syncHold))

	asyncHold := &model.BillingHold{UpstreamTaskId: "task-1"}
	assert.Equal(t, int64(billingHoldAsyncReconcileDelaySec), billingHoldReconcileDelaySecFor(asyncHold))
	assert.Equal(t, int64(billingHoldAsyncUnknownMaxAgeSec), billingHoldUnknownMaxAgeFor(asyncHold))
}

func TestRunBillingHoldReconcileUnknownReschedulesWithoutCharge(t *testing.T) {
	truncate(t)

	now := common.GetTimestamp()
	hold := &model.BillingHold{
		RequestId:        "req-unknown-reschedule",
		UserId:           101,
		PreConsumedQuota: 500,
		ErrorStatus:      http.StatusBadGateway,
		ErrorCode:        string(types.ErrorCodeDoRequestFailed),
		ErrorMessage:     "upstream request state unknown",
		Status:           model.BillingHoldStatusPending,
		CreatedAt:        now,
		ReconcileAfter:   now,
	}
	require.NoError(t, model.CreateBillingHold(hold))

	runBillingHoldReconcile(hold.Id)

	updated, err := model.GetBillingHoldById(hold.Id)
	require.NoError(t, err)
	require.Equal(t, model.BillingHoldStatusPending, updated.Status)
	require.GreaterOrEqual(t, updated.ReconcileAfter, now+int64(billingHoldUnknownRetrySec))
	require.Contains(t, updated.VerifyDetail, "keep pending")

	var count int64
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).
		Where("request_id = ?", hold.RequestId).
		Count(&count).Error)
	require.Zero(t, count)
}

func TestRunBillingHoldReconcileExpiredUnknownRefunds(t *testing.T) {
	truncate(t)

	user := &model.User{
		Id:       103,
		Username: "expired_hold_user",
		Quota:    1000,
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, model.DB.Create(user).Error)

	now := common.GetTimestamp()
	hold := &model.BillingHold{
		RequestId:        "req-unknown-expired",
		UserId:           user.Id,
		PreConsumedQuota: 500,
		ErrorStatus:      http.StatusBadGateway,
		ErrorCode:        string(types.ErrorCodeDoRequestFailed),
		ErrorMessage:     "upstream request state unknown",
		Status:           model.BillingHoldStatusPending,
		CreatedAt:        now - billingHoldSyncUnknownMaxAgeSec,
		ReconcileAfter:   now,
	}
	require.NoError(t, model.CreateBillingHold(hold))

	runBillingHoldReconcile(hold.Id)

	updated, err := model.GetBillingHoldById(hold.Id)
	require.NoError(t, err)
	require.Equal(t, model.BillingHoldStatusRefunded, updated.Status)
	require.Contains(t, updated.VerifyDetail, "customer-safe refund")

	var updatedUser model.User
	require.NoError(t, model.DB.Select("quota").Where("id = ?", user.Id).First(&updatedUser).Error)
	require.Equal(t, 1500, updatedUser.Quota)

	var refundLog model.Log
	require.NoError(t, model.LOG_DB.
		Where("request_id = ? AND type = ?", hold.RequestId, model.LogTypeRefund).
		First(&refundLog).Error)
	require.Equal(t, 500, refundLog.Quota)
}

func TestBillingHoldAPIError(t *testing.T) {
	hold := &model.BillingHold{
		ErrorStatus:  http.StatusBadGateway,
		ErrorCode:    string(types.ErrorCodeBadResponseStatusCode),
		ErrorMessage: "upstream bad gateway",
	}
	err := billingHoldAPIError(hold)
	if ClassifyUpstreamChargeConfidence(err) != UpstreamChargeAmbiguous {
		t.Fatalf("expected ambiguous")
	}
}

func TestConfirmBillingHold_WritesConsumeLog(t *testing.T) {
	truncate(t)

	user := &model.User{Id: 101, Username: "hold_user", Quota: 10000, UsedQuota: 0, Status: common.UserStatusEnabled}
	require.NoError(t, model.DB.Create(user).Error)

	hold := &model.BillingHold{
		RequestId:        "req-confirm-1",
		UserId:           101,
		ModelName:        "gpt-4o",
		ChannelId:        7,
		PreConsumedQuota: 500,
		Status:           model.BillingHoldStatusPending,
	}
	require.NoError(t, model.CreateBillingHold(hold))
	claimed, err := model.ClaimBillingHold(hold.Id)
	require.NoError(t, err)
	require.True(t, claimed)

	hold, err = model.GetBillingHoldById(hold.Id)
	require.NoError(t, err)

	err = ConfirmBillingHold(hold, "upstream unverified")
	require.NoError(t, err)

	var updated model.User
	require.NoError(t, model.DB.Select("used_quota").Where("id = ?", 101).First(&updated).Error)
	assert.Equal(t, 500, updated.UsedQuota)

	var log model.Log
	require.NoError(t, model.LOG_DB.Order("id desc").First(&log).Error)
	assert.Equal(t, model.LogTypeConsume, log.Type)
	assert.Equal(t, "req-confirm-1", log.RequestId)
	assert.Equal(t, 500, log.Quota)
	assert.Equal(t, "gpt-4o", log.ModelName)
}

func TestConfirmBillingHold_SkipsDuplicateConsumeLog(t *testing.T) {
	truncate(t)

	user := &model.User{Id: 102, Username: "hold_user2", Quota: 10000, UsedQuota: 500, Status: common.UserStatusEnabled}
	require.NoError(t, model.DB.Create(user).Error)

	existing := &model.Log{
		UserId:    102,
		Type:      model.LogTypeConsume,
		RequestId: "req-dup-1",
		Quota:     500,
		CreatedAt: common.GetTimestamp(),
	}
	require.NoError(t, model.LOG_DB.Create(existing).Error)

	hold := &model.BillingHold{
		RequestId:        "req-dup-1",
		UserId:           102,
		PreConsumedQuota: 500,
		Status:           model.BillingHoldStatusPending,
	}
	require.NoError(t, model.CreateBillingHold(hold))
	claimed, err := model.ClaimBillingHold(hold.Id)
	require.NoError(t, err)
	require.True(t, claimed)
	hold, err = model.GetBillingHoldById(hold.Id)
	require.NoError(t, err)

	err = ConfirmBillingHold(hold, "already logged")
	require.NoError(t, err)

	var updated model.User
	require.NoError(t, model.DB.Select("used_quota").Where("id = ?", 102).First(&updated).Error)
	assert.Equal(t, 500, updated.UsedQuota)

	var count int64
	model.LOG_DB.Model(&model.Log{}).Where("user_id = ? AND type = ?", 102, model.LogTypeConsume).Count(&count)
	assert.Equal(t, int64(1), count)
}
