package model

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestVideoSubmissionReconcileOriginalTaskWithoutBillingMutation(t *testing.T) {
	submissionTestDB(t)
	row, _, err := ClaimVideoSubmission(claimFixture("recovery"))
	require.NoError(t, err)
	require.NoError(t, MarkVideoSubmissionSending(row.ID, 273))
	require.NoError(t, FinishVideoSubmission(row.ID, VideoSubmissionUnknown, "", false))
	// Missing original task remains unknown, including after repeated scans.
	for i := 0; i < 2; i++ {
		require.NoError(t, ReconcileVideoSubmissions(200))
	}
	got, err := GetVideoSubmission(row.UserID, row.ID)
	require.NoError(t, err)
	require.Equal(t, VideoSubmissionUnknown, got.Status)
	task := Task{TaskID: row.TaskID, UserId: row.UserID, ChannelId: 273, Quota: 12345, Status: TaskStatusInProgress, Properties: Properties{OriginModelName: row.Model}, PrivateData: TaskPrivateData{UpstreamTaskID: "provider-original"}}
	require.NoError(t, DB.Create(&task).Error)
	// Task alone is not enough to advertise a ready billable task.
	require.NoError(t, ReconcileVideoSubmissions(200))
	got, err = GetVideoSubmission(row.UserID, row.ID)
	require.NoError(t, err)
	require.False(t, got.TaskReady)
	raw, err := common.Marshal(SeedanceBillingDetails{TaskID: row.TaskID, Model: row.Model})
	require.NoError(t, err)
	receipt := SeedanceBillingReceipt{ID: "receipt-original", TaskID: row.TaskID, UserID: row.UserID, Status: "pending", InitialQuota: 12345, Details: raw}
	require.NoError(t, DB.Create(&receipt).Error)
	// Lookup repairs immediately; background scans and repeats are idempotent.
	got, err = GetVideoSubmission(row.UserID, row.ID)
	require.NoError(t, err)
	require.Equal(t, VideoSubmissionAccepted, got.Status)
	require.True(t, got.TaskReady)
	require.Equal(t, "provider-original", got.UpstreamTaskID)
	require.NoError(t, ReconcileVideoSubmissions(200))
	var tasks, receipts int64
	require.NoError(t, DB.Model(&Task{}).Count(&tasks).Error)
	require.EqualValues(t, 1, tasks)
	require.NoError(t, DB.Model(&SeedanceBillingReceipt{}).Count(&receipts).Error)
	require.EqualValues(t, 1, receipts)
	var saved SeedanceBillingReceipt
	require.NoError(t, DB.First(&saved).Error)
	require.Equal(t, "pending", saved.Status)
	require.Equal(t, 12345, saved.InitialQuota)
	require.Equal(t, 0, saved.FinalQuota)
}

func TestVideoSubmissionReconcileRejectsIdentityConflicts(t *testing.T) {
	for _, conflict := range []string{"user", "channel", "model", "upstream", "duplicate_task", "receipt_model", "receipt_task"} {
		t.Run(conflict, func(t *testing.T) {
			submissionTestDB(t)
			row, _, err := ClaimVideoSubmission(claimFixture("conflict"))
			require.NoError(t, err)
			require.NoError(t, MarkVideoSubmissionSending(row.ID, 273))
			require.NoError(t, FinishVideoSubmission(row.ID, VideoSubmissionUnknown, "", false))
			task := Task{TaskID: row.TaskID, UserId: row.UserID, ChannelId: 273, Properties: Properties{OriginModelName: row.Model}, PrivateData: TaskPrivateData{UpstreamTaskID: "original"}}
			details := SeedanceBillingDetails{TaskID: row.TaskID, Model: row.Model}
			switch conflict {
			case "user":
				task.UserId++
			case "channel":
				task.ChannelId++
			case "model":
				task.Properties.OriginModelName = "other"
			case "upstream":
				require.NoError(t, DB.Model(&VideoSubmission{}).Where("id = ?", row.ID).Update("upstream_task_id", "different").Error)
			case "receipt_model":
				details.Model = "different"
			case "receipt_task":
				details.TaskID = "different"
			}
			require.NoError(t, DB.Create(&task).Error)
			if conflict == "duplicate_task" {
				task.ID = 0
				require.NoError(t, DB.Create(&task).Error)
			}
			raw, err := common.Marshal(details)
			require.NoError(t, err)
			require.NoError(t, DB.Create(&SeedanceBillingReceipt{ID: "bill", TaskID: row.TaskID, UserID: row.UserID, Details: raw}).Error)
			require.NoError(t, ReconcileVideoSubmissions(200))
			got, err := GetVideoSubmission(row.UserID, row.ID)
			require.NoError(t, err)
			require.Equal(t, VideoSubmissionUnknown, got.Status)
			require.False(t, got.TaskReady)
		})
	}
}
