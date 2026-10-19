package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

const lookupTestUUID = "11111111-1111-4111-8111-111111111111"

func lookupTestOriginal(t *testing.T, settled bool) VideoSubmission {
	t.Helper()
	submissionTestDB(t)
	prepareVideoFeeFinancialTests(t)
	require.NoError(t, DB.AutoMigrate(&Channel{}, &User{}))
	require.NoError(t, DB.Create(&Channel{Id: 273, Key: "original-key"}).Error)
	require.NoError(t, DB.Create(&User{Id: 7, Username: "original-owner", Quota: 9000}).Error)
	row, _, err := ClaimVideoSubmission(claimFixture("lookup-original"))
	require.NoError(t, err)
	task := Task{TaskID: row.TaskID, UserId: row.UserID, ChannelId: 273, Quota: 40, Status: TaskStatusSubmitted, Properties: Properties{OriginModelName: row.Model}, SubmitTime: time.Now().Add(-time.Minute).Unix()}
	snapshot := VideoSubmissionRecovery{Task: task, PrivateData: TaskPrivateData{TokenId: 9, BillingSource: "wallet"}, CredentialFingerprint: VideoFeeCredentialFingerprint("original-key")}
	prepareVideoFeeRecoveryFixture(t, &snapshot, row)
	raw, err := common.Marshal(snapshot)
	require.NoError(t, err)
	require.NoError(t, DB.Model(row).Updates(map[string]any{"channel_id": 273, "status": VideoSubmissionUnknown, "recovery_json": string(raw), "recovery_settled": settled, "provider_lookup_lease": "lease", "updated_at": time.Now().Add(-time.Minute).Unix()}).Error)
	require.NoError(t, DB.First(row, "id = ?", row.ID).Error)
	return *row
}

func lookupTestObservation() VideoFeeSubmissionObservation {
	id := lookupTestUUID
	return VideoFeeSubmissionObservation{Object: "submission", Key: "test-original-key", Acceptance: "accepted", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), TaskID: &id, Task: &VideoFeeTaskObservation{ID: id, Status: "submission_unknown"}}
}

func TestVideoFeeAcceptedOriginalPreservesIdentityAndMoney(t *testing.T) {
	row := lookupTestOriginal(t, true)
	o := lookupTestObservation()
	for i := 0; i < 2; i++ {
		require.NoError(t, ApplyVideoFeeObservation(&row, &o, o.Key))
	}
	var task Task
	require.NoError(t, DB.Where("task_id = ?", row.TaskID).First(&task).Error)
	require.True(t, task.PrivateData.ProviderObservationOnly)
	require.Equal(t, row.CreatedAt, task.CreatedAt)
	require.Equal(t, lookupTestUUID, task.PrivateData.UpstreamTaskID)
	require.Empty(t, GetAllUnFinishSyncTasks(1))
	require.Empty(t, GetTimedOutUnfinishedTasks(time.Now().Unix(), 1))
	require.Equal(t, 40, task.Quota)
	require.EqualValues(t, TaskStatusInProgress, task.Status)
	var saved VideoSubmission
	require.NoError(t, DB.First(&saved, "id = ?", row.ID).Error)
	require.Equal(t, VideoSubmissionAccepted, saved.Status)
	require.True(t, saved.TaskReady)
	// A later stored result advances the same local original; no generation/poll POST.
	result := "https://seedance2026.vip/v1/videos/" + lookupTestUUID + "/content"
	o.Task.Status = "succeeded"
	o.Task.ResultURL = &result
	o.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	require.NoError(t, ApplyVideoFeeObservation(&row, &o, o.Key))
	require.NoError(t, DB.First(&task, task.ID).Error)
	require.EqualValues(t, TaskStatusSuccess, task.Status)
	require.Equal(t, result, task.PrivateData.ResultURL)
	var count int64
	require.NoError(t, DB.Model(&Task{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	var user User
	require.NoError(t, DB.First(&user, 7).Error)
	require.Equal(t, 9000, user.Quota)
	// Contradictory later rejection cannot erase an accepted task or permit resubmit.
	o.Acceptance = "not_accepted"
	o.Task = nil
	o.TaskID = nil
	o.NewSubmissionSafe = true
	o.Billing = lookupTestNotApplicable()
	require.Error(t, ApplyVideoFeeObservation(&row, &o, o.Key))
}

func lookupTestNotApplicable() VideoFeeBillingFacts {
	f := VideoFeeLedgerFacts{Status: "not_applicable"}
	return VideoFeeBillingFacts{Reservation: "none", Hold: f, Release: f, Charge: f, Refund: f}
}

func TestVideoFeeUnknownAndFencedRejectionNeedFinancialCompletion(t *testing.T) {
	row := lookupTestOriginal(t, true)
	for _, status := range []string{"acceptance_unknown", "processing"} {
		o := VideoFeeSubmissionObservation{Object: "submission", Key: "test-original-key", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Acceptance: status}
		require.NoError(t, ApplyVideoFeeObservation(&row, &o, o.Key))
		var got VideoSubmission
		require.NoError(t, DB.First(&got, "id = ?", row.ID).Error)
		require.Equal(t, VideoSubmissionUnknown, got.Status)
		require.False(t, got.TaskReady)
	}
	o := VideoFeeSubmissionObservation{Object: "submission", Key: "test-original-key", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Acceptance: "not_accepted", NewSubmissionSafe: true, Billing: lookupTestNotApplicable()}
	// Committed funds alone cannot authorize a new submission.
	require.NoError(t, ApplyVideoFeeObservation(&row, &o, o.Key))
	var blocked VideoSubmission
	require.NoError(t, DB.First(&blocked, "id = ?", row.ID).Error)
	require.Equal(t, VideoSubmissionUnknown, blocked.Status)
	require.NoError(t, DB.Model(&row).Update("provider_refund_completed", true).Error)
	require.NoError(t, ApplyVideoFeeObservation(&row, &o, o.Key))
	var got VideoSubmission
	require.NoError(t, DB.First(&got, "id = ?", row.ID).Error)
	require.Equal(t, VideoSubmissionNotAccepted, got.Status)
	var tasks int64
	require.NoError(t, DB.Model(&Task{}).Count(&tasks).Error)
	require.Zero(t, tasks)
}

func TestVideoFeeHistoricalRowsAndLeaseCannotBeReplayed(t *testing.T) {
	row := lookupTestOriginal(t, true)
	require.NoError(t, DB.Model(&row).UpdateColumn("provider_lookup_enabled", false).Error)
	rows, err := ClaimVideoFeeLookups(time.Now().Unix(), 5)
	require.NoError(t, err)
	require.Empty(t, rows)
	require.NoError(t, DB.Model(&row).UpdateColumn("provider_lookup_enabled", true).Error)
	rows, err = ClaimVideoFeeLookups(time.Now().Unix(), 5)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	second, err := ClaimVideoFeeLookups(time.Now().Unix(), 5)
	require.NoError(t, err)
	require.Empty(t, second)
	o := lookupTestObservation()
	require.Error(t, ApplyVideoFeeObservation(&row, &o, o.Key)) // old lease
	require.NoError(t, ApplyVideoFeeObservation(&rows[0], &o, o.Key))
	rows[0].ProviderLookupAttempts = 100
	require.NoError(t, ScheduleVideoFeeLookup(&rows[0], time.Now().Unix(), nil))
	var got VideoSubmission
	require.NoError(t, DB.First(&got, "id = ?", row.ID).Error)
	require.GreaterOrEqual(t, got.ProviderLookupNext, time.Now().Unix()+299)
}

func TestVideoFeeMalformedConflictingAndStaleEvidenceFailsClosed(t *testing.T) {
	row := lookupTestOriginal(t, true)
	valid := lookupTestObservation()
	for _, change := range []func(*VideoFeeSubmissionObservation){
		func(o *VideoFeeSubmissionObservation) { o.Key = "other-account-key" },
		func(o *VideoFeeSubmissionObservation) { o.TaskID = nil },
		func(o *VideoFeeSubmissionObservation) { o.NewSubmissionSafe = true },
		func(o *VideoFeeSubmissionObservation) { o.Task.Status = "new-unknown-status" },
		func(o *VideoFeeSubmissionObservation) { o.ObservedAt = "invalid" },
	} {
		o := valid
		task := *valid.Task
		o.Task = &task
		change(&o)
		require.Error(t, ApplyVideoFeeObservation(&row, &o, valid.Key))
	}
	require.NoError(t, ApplyVideoFeeObservation(&row, &valid, valid.Key))
	valid.ObservedAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	require.Error(t, ApplyVideoFeeObservation(&row, &valid, valid.Key))
}

func TestVideoFeeUnfinishedRefundCannotPublishOrAuthorizeResubmit(t *testing.T) {
	row := lookupTestOriginal(t, false)
	o := lookupTestObservation()
	require.NoError(t, ApplyVideoFeeObservation(&row, &o, o.Key))
	var got VideoSubmission
	require.NoError(t, DB.First(&got, "id = ?", row.ID).Error)
	require.Equal(t, VideoSubmissionUnknown, got.Status)
	require.False(t, got.TaskReady)
	var count int64
	require.NoError(t, DB.Model(&Task{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, DB.Model(&got).Update("provider_refund_completed", true).Error)
	require.NoError(t, ApplyVideoFeeObservation(&row, &o, o.Key))
	require.NoError(t, DB.First(&got, "id = ?", row.ID).Error)
	require.True(t, got.TaskReady)
	require.Equal(t, VideoSubmissionAccepted, got.Status)
	var user User
	require.NoError(t, DB.First(&user, 7).Error)
	require.Equal(t, 9000, user.Quota)
}
