package model

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func recoveryFixture(t *testing.T, name string, channelID int) (*VideoSubmission, *Task, *SeedanceBillingReceipt) {
	t.Helper()
	row, _, err := ClaimVideoSubmission(&VideoSubmission{ID: "restore", UserID: 7, Model: name, TaskID: GenerateTaskID(), Fingerprint: "request", RequestID: "original-request"})
	require.NoError(t, err)
	require.NoError(t, DB.AutoMigrate(&Channel{}, &BillingHold{}))
	require.NoError(t, DB.Create(&Channel{Id: channelID, Key: "original-supplier-key\nother-key"}).Error)
	task := &Task{TaskID: row.TaskID, UserId: 7, ChannelId: channelID, Quota: 12345, Status: TaskStatusNotStart, Progress: "0%", SubmitTime: time.Now().Unix(), Properties: Properties{OriginModelName: name}, PrivateData: TaskPrivateData{Key: "original-supplier-key", TokenId: 23, BillingSource: "wallet", SeedanceBillingReceiptEnabled: true, BillingContext: &TaskBillingContext{SeedanceTariff: true, ModelPrice: 0.123, OriginModelName: name}, SeedanceRequest: map[string]any{"duration": 10, "video_input_seconds": 4}}}
	raw, err := common.Marshal(SeedanceBillingDetails{TaskID: row.TaskID, Model: name, InitialQuotaUnits: 12345, RequestedOutputSeconds: 10, BillableInputVideoSeconds: 4, EffectiveUnitRate: "0.123", TariffRevision: "frozen-revision"})
	require.NoError(t, err)
	receipt := &SeedanceBillingReceipt{ID: "bill_" + row.TaskID, TaskID: row.TaskID, UserID: 7, InitialQuota: 12345, Status: "pending", Details: raw}
	require.NoError(t, PrepareVideoSubmissionRecovery(row, task, receipt))
	require.NoError(t, MarkVideoSubmissionSending(row.ID, channelID))
	return row, task, receipt
}

func TestVideoRecoveryRestoresOriginalAcrossSeedanceModelsAndChannels(t *testing.T) {
	for _, name := range []string{"seedance-2.0", "seedance-2.0-fast", "seedance-2.0-mini", "seedance-2.5"} {
		for _, channelID := range []int{153, 272, 273, 274, 292, 297} {
			t.Run(fmt.Sprintf("%s/%d", name, channelID), func(t *testing.T) {
				submissionTestDB(t)
				row, original, receipt := recoveryFixture(t, name, channelID)
				var stored VideoSubmission
				require.NoError(t, DB.First(&stored, "id = ?", row.ID).Error)
				require.NotContains(t, stored.RecoveryJSON, "original-supplier-key")
				require.NotContains(t, stored.RecoveryJSON, "other-key")
				public, err := common.Marshal(stored)
				require.NoError(t, err)
				require.NotContains(t, string(public), "frozen-revision")
				require.NoError(t, AcceptVideoSubmission(row.ID, "original-upstream", []byte(`{"id":"original-upstream"}`)))
				// A crash before a confirmed financial checkpoint cannot authorize billing.
				require.NoError(t, ReconcileVideoSubmissions(200))
				var count int64
				require.NoError(t, DB.Model(&Task{}).Count(&count).Error)
				require.Zero(t, count)
				require.NoError(t, MarkVideoSubmissionBillingCommitted(row.ID))
				// Simulate a process restart: only DB evidence is available to this worker.
				require.NoError(t, ReconcileVideoSubmissions(200))
				recovered, err := GetVideoSubmission(row.UserID, row.ID)
				require.NoError(t, err)
				require.True(t, recovered.TaskReady)
				var task Task
				require.NoError(t, DB.First(&task, "task_id = ?", row.TaskID).Error)
				require.Equal(t, original.TaskID, task.TaskID)
				require.Equal(t, "original-upstream", task.PrivateData.UpstreamTaskID)
				require.Equal(t, "original-supplier-key", task.PrivateData.Key)
				require.Equal(t, 12345, task.Quota)
				require.Equal(t, 0.123, task.PrivateData.BillingContext.ModelPrice)
				var saved SeedanceBillingReceipt
				require.NoError(t, DB.First(&saved, "id = ?", receipt.ID).Error)
				require.Equal(t, receipt.Details, saved.Details)
				// A late controller and repeated worker runs must not insert or charge twice.
				original.PrivateData.UpstreamTaskID = "original-upstream"
				require.NoError(t, InsertVideoSubmissionTask(original, receipt, row.ID))
				require.NoError(t, ReconcileVideoSubmissions(200))
				require.NoError(t, DB.Model(&Task{}).Count(&count).Error)
				require.EqualValues(t, 1, count)
				require.NoError(t, DB.Model(&SeedanceBillingReceipt{}).Count(&count).Error)
				require.EqualValues(t, 1, count)
			})
		}
	}
}

func TestVideoRecoveryDoesNotGuessUnknownOrRebillRefundedCustomers(t *testing.T) {
	for _, reason := range []string{"unknown", "billing_uncommitted", "refunded", "pending_hold", "credential_rotated", "identity_conflict", "receipt_conflict"} {
		t.Run(reason, func(t *testing.T) {
			submissionTestDB(t)
			row, _, _ := recoveryFixture(t, "seedance-2.5", 273)
			if reason == "unknown" {
				require.NoError(t, FinishVideoSubmission(row.ID, VideoSubmissionUnknown, "", false))
			} else {
				require.NoError(t, AcceptVideoSubmission(row.ID, "original-upstream", []byte(`{}`)))
				if reason != "billing_uncommitted" {
					require.NoError(t, MarkVideoSubmissionBillingCommitted(row.ID))
				}
			}
			switch reason {
			case "refunded", "pending_hold":
				status := BillingHoldStatusRefunded
				if reason == "pending_hold" {
					status = BillingHoldStatusPending
				}
				require.NoError(t, DB.Create(&BillingHold{RequestId: row.RequestID, UserId: row.UserID, Status: status, PreConsumedQuota: 12345}).Error)
			case "credential_rotated":
				require.NoError(t, DB.Model(&Channel{}).Where("id = ?", 273).Update("key", "replacement-key").Error)
			case "identity_conflict", "receipt_conflict":
				var stored VideoSubmission
				require.NoError(t, DB.First(&stored, "id = ?", row.ID).Error)
				var snapshot VideoSubmissionRecovery
				require.NoError(t, common.UnmarshalJsonStr(stored.RecoveryJSON, &snapshot))
				if reason == "identity_conflict" {
					snapshot.Task.UserId++
				} else {
					snapshot.Receipt.InitialQuota++
				}
				raw, err := common.Marshal(snapshot)
				require.NoError(t, err)
				require.NoError(t, DB.Model(&stored).Update("recovery_json", string(raw)).Error)
			}
			err := ReconcileVideoSubmissions(200)
			if reason == "identity_conflict" || reason == "receipt_conflict" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			var count int64
			require.NoError(t, DB.Model(&Task{}).Count(&count).Error)
			require.Zero(t, count)
			require.NoError(t, DB.Model(&SeedanceBillingReceipt{}).Count(&count).Error)
			require.Zero(t, count)
			var stored VideoSubmission
			require.NoError(t, DB.First(&stored, "id = ?", row.ID).Error)
			require.False(t, stored.TaskReady)
		})
	}
}

func TestVideoRecoveryConcurrentControllerAndWorker(t *testing.T) {
	submissionTestDB(t)
	row, task, receipt := recoveryFixture(t, "seedance-2.5", 297)
	require.NoError(t, AcceptVideoSubmission(row.ID, "original", []byte(`{}`)))
	require.NoError(t, MarkVideoSubmissionBillingCommitted(row.ID))
	task.PrivateData.UpstreamTaskID = "original"
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); errs <- RestoreVideoSubmission(row.UserID, row.ID) }()
	go func() { defer wg.Done(); errs <- InsertVideoSubmissionTask(task, receipt, row.ID) }()
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var count int64
	require.NoError(t, DB.Model(&Task{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestVideoReconcileExpiresLeasesWithoutUpstreamEvidence(t *testing.T) {
	submissionTestDB(t)
	for _, status := range []string{VideoSubmissionPending, VideoSubmissionSubmitting} {
		row, _, err := ClaimVideoSubmission(claimFixture(status))
		require.NoError(t, err)
		require.NoError(t, DB.Model(&VideoSubmission{}).Where("id = ?", row.ID).Updates(map[string]any{"status": status, "deadline": time.Now().Unix() - 1}).Error)
	}
	require.NoError(t, ReconcileVideoSubmissions(200))
	for _, status := range []string{VideoSubmissionPending, VideoSubmissionSubmitting} {
		var row VideoSubmission
		require.NoError(t, DB.First(&row, "id = ?", status).Error)
		want := VideoSubmissionNotAccepted
		if status == VideoSubmissionSubmitting {
			want = VideoSubmissionUnknown
		}
		require.Equal(t, want, row.Status)
		require.Error(t, MarkVideoSubmissionSending(row.ID, 273))
	}
}
