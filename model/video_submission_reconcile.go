package model

import (
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// ReconcileVideoSubmission uses only an already persisted original task and
// its billing receipt. Absence, a timeout or a refund is never rejection proof.
// It cannot create a task, call an upstream, reserve funds or change a bill.
func ReconcileVideoSubmission(userID int, id string) error {
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

// Limit scans to receipts with existing tasks; unknowable upstream submissions
// cannot starve recoverable receipts and are never converted to not_accepted.
func ReconcileVideoSubmissions(limit int) error {
	var rows []VideoSubmission
	err := DB.Model(&VideoSubmission{}).Select("video_submissions.*").Where("task_ready = ? AND status IN ?", false, []string{VideoSubmissionUnknown, VideoSubmissionSubmitting, VideoSubmissionAccepted}).Where("EXISTS (SELECT 1 FROM tasks WHERE tasks.task_id = video_submissions.task_id AND tasks.user_id = video_submissions.user_id)").Order("updated_at ASC, id ASC").Limit(limit).Find(&rows).Error
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := ReconcileVideoSubmission(row.UserID, row.ID); err != nil {
			return err
		}
	}
	return nil
}
