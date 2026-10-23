package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestVideoFeeFailedOriginalRefundConvergesExactlyOnce(t *testing.T) {
	truncate(t)
	require.NoError(t, model.DB.AutoMigrate(&model.VideoSubmission{}, &model.VideoSubmissionRefund{}))
	user := model.User{Id: 104, Username: "failed_original_owner", Quota: 1000, Status: common.UserStatusEnabled}
	require.NoError(t, model.DB.Create(&user).Error)
	t.Cleanup(func() { model.DB.Where("id = ?", "failed-original-refund").Delete(&model.VideoSubmission{}) })
	now := time.Now().Unix()
	row := model.VideoSubmission{ID: "failed-original-refund", UserID: user.Id, RequestID: "req-failed-original-refund", TaskID: "task_failed_original", ChannelID: 273, Model: "seedance-2.5", Status: model.VideoSubmissionUnknown, CreatedAt: now}
	row.ProviderLookupEnabled = true
	snapshot := model.VideoSubmissionRecovery{Task: model.Task{TaskID: row.TaskID, UserId: user.Id, ChannelId: 273, Quota: 500}}
	raw, e := common.Marshal(snapshot)
	require.NoError(t, e)
	row.RecoveryJSON = string(raw)
	id := "11111111-1111-4111-8111-111111111111"
	obs := model.VideoFeeSubmissionObservation{Object: "submission", Key: "apimaster-" + row.TaskID, Status: "failed", Acceptance: "accepted", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), TaskID: &id, Task: &model.VideoFeeTaskObservation{ID: id, Status: "failed"}, Billing: model.VideoFeeBillingFacts{Status: "recorded", Currency: "CNY", Reservation: "released", Hold: model.VideoFeeLedgerFacts{Status: "recorded", Entries: []model.VideoFeeLedgerEntry{{ID: "h", Type: "hold", Credit: "100", Yuan: "0.01"}}}, Release: model.VideoFeeLedgerFacts{Status: "recorded", Entries: []model.VideoFeeLedgerEntry{{ID: "r", Type: "release", Credit: "100", Yuan: "0.01"}}}, Charge: model.VideoFeeLedgerFacts{Status: "unknown"}, Refund: model.VideoFeeLedgerFacts{Status: "unknown"}}}
	raw, e = common.Marshal(obs)
	require.NoError(t, e)
	row.ProviderObservation = model.VideoFeeEvidenceJSON(raw)
	require.NoError(t, model.DB.Create(&row).Error)
	hold := &model.BillingHold{RequestId: row.RequestID, UserId: user.Id, ChannelId: 273, PreConsumedQuota: 500, ErrorStatus: 502, ErrorCode: "do_request_failed", Status: model.BillingHoldStatusPending, CreatedAt: now, ReconcileAfter: now}
	require.NoError(t, model.CreateBillingHold(hold))
	decision, _ := VerifyBillingHoldUpstreamCharge(hold)
	require.Equal(t, BillingHoldDecisionRefund, decision)
	runBillingHoldReconcile(hold.Id)
	runBillingHoldReconcile(hold.Id)
	saved, e := model.GetBillingHoldById(hold.Id)
	require.NoError(t, e)
	require.Equal(t, model.BillingHoldStatusRefunded, saved.Status)
	require.NoError(t, model.DB.First(&user, user.Id).Error)
	require.Equal(t, 1500, user.Quota)
	var n int64
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("request_id = ? AND type = ?", row.RequestID, model.LogTypeRefund).Count(&n).Error)
	require.EqualValues(t, 1, n)
	require.Contains(t, saved.VerifyDetail, "supplier final charge unverified")
}
