package model

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The snapshot stores a credential fingerprint, never a duplicate supplier key.
// PrivateData must be explicit because Task intentionally hides it from JSON.
type VideoSubmissionRecovery struct {
	Task                  Task                    `json:"task"`
	PrivateData           TaskPrivateData         `json:"private_data"`
	Receipt               *SeedanceBillingReceipt `json:"receipt"`
	CredentialFingerprint string                  `json:"credential_fingerprint"`
}

func PrepareVideoSubmissionRecovery(row *VideoSubmission, task *Task, receipt *SeedanceBillingReceipt) error {
	if row == nil || task == nil || task.TaskID != row.TaskID || task.UserId != row.UserID || task.Properties.OriginModelName != row.Model || task.ChannelId <= 0 || receipt == nil {
		return ErrVideoSubmissionClaimLost
	}
	snapshot := VideoSubmissionRecovery{Task: *task, PrivateData: task.PrivateData, Receipt: receipt}
	snapshot.CredentialFingerprint = fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimSpace(snapshot.PrivateData.Key))))
	snapshot.PrivateData.Key = ""
	raw, err := common.Marshal(snapshot)
	if err != nil {
		return err
	}
	res := DB.Model(&VideoSubmission{}).Where("id = ? AND owner_token = ? AND status = ? AND deadline > ?", row.ID, row.OwnerToken, VideoSubmissionPending, time.Now().Unix()).Updates(map[string]any{"recovery_json": string(raw), "recovery_settled": false, "response_json": "", "channel_id": task.ChannelId})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return ErrVideoSubmissionClaimLost
	}
	return nil
}

// Save the original acceptance before settling billing or publishing a task.
func AcceptVideoSubmission(id, upstreamID string, data []byte) error {
	if strings.TrimSpace(upstreamID) == "" {
		return ErrVideoSubmissionClaimLost
	}
	res := DB.Model(&VideoSubmission{}).Where("id = ? AND status IN ? AND (upstream_task_id = ? OR upstream_task_id = ?)", id, []string{VideoSubmissionSubmitting, VideoSubmissionUnknown}, "", upstreamID).Updates(map[string]any{"status": VideoSubmissionAccepted, "upstream_task_id": upstreamID, "response_json": string(data), "updated_at": time.Now().Unix()})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return ErrVideoSubmissionClaimLost
	}
	return nil
}

func MarkVideoSubmissionBillingCommitted(id string) error {
	res := DB.Model(&VideoSubmission{}).Where("id = ? AND status = ? AND upstream_task_id <> ?", id, VideoSubmissionAccepted, "").Update("recovery_settled", true)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return ErrVideoSubmissionClaimLost
	}
	return nil
}

// Restore only an acknowledged, financially committed original submission.
// This path has no generation call, debit, refund or consumption log replay.
// Missing/rotated credentials and uncertain billing remain for reconciliation.
func restoreVideoSubmission(tx *gorm.DB, row *VideoSubmission) error {
	if row.TaskReady || row.Status != VideoSubmissionAccepted || !row.RecoverySettled || row.RecoveryJSON == "" || row.UpstreamTaskID == "" {
		return nil
	}
	var snapshot VideoSubmissionRecovery
	if err := common.UnmarshalJsonStr(row.RecoveryJSON, &snapshot); err != nil {
		return fmt.Errorf("invalid video recovery snapshot")
	}
	task := snapshot.Task
	task.PrivateData = snapshot.PrivateData
	if task.ID != 0 || task.TaskID != row.TaskID || task.UserId != row.UserID || task.ChannelId != row.ChannelID || task.Properties.OriginModelName != row.Model || task.Quota < 0 || task.PrivateData.UpstreamTaskID != "" || snapshot.Receipt == nil {
		return ErrVideoSubmissionClaimLost
	}
	receipt := snapshot.Receipt
	var details SeedanceBillingDetails
	if receipt.TaskID != row.TaskID || receipt.UserID != row.UserID || receipt.InitialQuota != task.Quota || receipt.Status != "pending" || common.Unmarshal(receipt.Details, &details) != nil || details.TaskID != row.TaskID || details.Model != row.Model {
		return ErrVideoSubmissionClaimLost
	}
	var refunded int64
	if err := tx.Model(&VideoSubmissionRefund{}).Where("id = ?", row.ID).Count(&refunded).Error; err != nil {
		return err
	}
	if refunded != 0 {
		return nil
	}
	// A historical/customer-safe refund or unresolved hold must never become a
	// billable polling task. Keep its evidence for explicit financial review.
	if row.RequestID != "" {
		var holds int64
		if err := tx.Model(&BillingHold{}).Where("request_id = ? AND user_id = ?", row.RequestID, row.UserID).Count(&holds).Error; err != nil {
			return err
		}
		if holds != 0 {
			return nil
		}
	}
	// Never overwrite an existing task (possibly already terminal) or a receipt.
	var count int64
	if err := tx.Model(&Task{}).Where("task_id = ?", row.TaskID).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return nil
	}
	if err := tx.Model(&SeedanceBillingReceipt{}).Where("task_id = ? OR id = ?", row.TaskID, receipt.ID).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return nil
	}
	var channel Channel
	if err := tx.Where("id = ?", row.ChannelID).First(&channel).Error; err != nil {
		return err
	}
	for _, key := range channel.GetKeys() {
		if fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimSpace(key)))) == snapshot.CredentialFingerprint {
			task.PrivateData.Key = key
			break
		}
	}
	if task.PrivateData.Key == "" {
		return nil
	}
	task.PrivateData.UpstreamTaskID = row.UpstreamTaskID
	task.Data = []byte(row.ResponseJSON)
	return insertVideoSubmissionTask(tx, &task, receipt, row.ID)
}

func RestoreVideoSubmission(userID int, id string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var row VideoSubmission
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", id, userID).First(&row).Error; err != nil {
			return err
		}
		return restoreVideoSubmission(tx, &row)
	})
}
