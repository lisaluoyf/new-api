package model

import (
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// ReconcileVideoSubmission restores acknowledged originals from frozen evidence
// or repairs their ready markers. A timeout or refund is never rejection proof.
// It never calls a generation endpoint, reserves funds or replays billing.
func ReconcileVideoSubmission(userID int, id string) error {
	if err := RestoreVideoSubmission(userID, id); err != nil {
		return err
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var row VideoSubmission
		if err := tx.Where("id = ? AND user_id = ?", id, userID).First(&row).Error; err != nil {
			return err
		}
		if row.TaskReady || (row.Status != VideoSubmissionUnknown && row.Status != VideoSubmissionSubmitting && row.Status != VideoSubmissionAccepted) {
			return nil
		}
		var tasks []Task
		if err := tx.Where("task_id = ? AND user_id = ?", row.TaskID, row.UserID).Limit(2).Find(&tasks).Error; err != nil {
			return err
		}
		if len(tasks) != 1 {
			return nil
		}
		task := tasks[0]
		upstream := task.PrivateData.UpstreamTaskID
		if upstream == "" || task.ChannelId != row.ChannelID || task.Properties.OriginModelName != row.Model || (row.UpstreamTaskID != "" && row.UpstreamTaskID != upstream) {
			return nil
		}
		var receipts []SeedanceBillingReceipt
		if err := tx.Where("task_id = ? AND user_id = ?", row.TaskID, row.UserID).Limit(2).Find(&receipts).Error; err != nil {
			return err
		}
		if len(receipts) != 1 {
			return nil
		}
		var details SeedanceBillingDetails
		if common.Unmarshal(receipts[0].Details, &details) != nil || details.Model != row.Model || details.TaskID != row.TaskID {
			return nil
		}
		return tx.Model(&VideoSubmission{}).Where("id = ? AND user_id = ? AND status = ? AND task_ready = ? AND channel_id = ? AND upstream_task_id = ?", row.ID, row.UserID, row.Status, false, row.ChannelID, row.UpstreamTaskID).Updates(map[string]any{"status": VideoSubmissionAccepted, "task_ready": true, "upstream_task_id": upstream, "updated_at": time.Now().Unix()}).Error
	})
}

// Scan recoverable frozen submissions and existing task evidence. Unknown
// acceptance never causes a resend or an inferred upstream rejection.
func ReconcileVideoSubmissions(limit int) error {
	if limit <= 0 {
		limit = 200
	}
	now := time.Now().Unix()
	for _, pair := range [][2]string{{VideoSubmissionPending, VideoSubmissionNotAccepted}, {VideoSubmissionSubmitting, VideoSubmissionUnknown}} {
		if err := DB.Model(&VideoSubmission{}).Where("status = ? AND deadline <= ?", pair[0], now).Updates(map[string]any{"status": pair[1], "updated_at": now}).Error; err != nil {
			return err
		}
	}
	var rows []VideoSubmission
	err := DB.Model(&VideoSubmission{}).Select("video_submissions.*").Where("task_ready = ? AND status IN ?", false, []string{VideoSubmissionUnknown, VideoSubmissionSubmitting, VideoSubmissionAccepted}).Where("(recovery_settled = ? AND recovery_json <> ?) OR EXISTS (SELECT 1 FROM tasks WHERE tasks.task_id = video_submissions.task_id AND tasks.user_id = video_submissions.user_id)", true, "").Order("updated_at ASC, id ASC").Limit(limit).Find(&rows).Error
	if err != nil {
		return err
	}
	var firstErr error
	for _, row := range rows {
		if err := ReconcileVideoSubmission(row.UserID, row.ID); err != nil && firstErr == nil {
			firstErr = err
		}
		// Rotate deferred/conflicting evidence behind other candidates.
		if err := DB.Model(&VideoSubmission{}).Where("id = ? AND task_ready = ?", row.ID, false).Update("updated_at", now).Error; err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
