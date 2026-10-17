package model

import (
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	VideoSubmissionPending      = "pending"
	VideoSubmissionSubmitting   = "submitting"
	VideoSubmissionAccepted     = "accepted"
	VideoSubmissionNotAccepted  = "not_accepted"
	VideoSubmissionUnknown      = "acceptance_unknown"
	VideoSubmissionLeaseSeconds = int64(1800)
)

// A durable claim is never deleted or reused. Only its creator may execute it.
// No API key, prompt, media URL or raw idempotency key is stored here.
type VideoSubmission struct {
	AttemptsJSON   string `json:"-" gorm:"type:text"`
	ID             string `json:"submission_id" gorm:"primaryKey;size:64"`
	UserID         int    `json:"-" gorm:"index"`
	OwnerToken     string `json:"-" gorm:"size:64"`
	Fingerprint    string `json:"-" gorm:"size:64"`
	Model          string `json:"model" gorm:"size:64"`
	TaskID         string `json:"task_id" gorm:"size:64;index"`
	RequestID      string `json:"request_id" gorm:"size:128"`
	Status         string `json:"status" gorm:"size:32"`
	ChannelID      int    `json:"-"`
	UpstreamTaskID string `json:"-" gorm:"size:128"`
	TaskReady      bool   `json:"task_ready"`
	CreatedAt      int64  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt      int64  `json:"updated_at" gorm:"autoUpdateTime"`
	Deadline       int64  `json:"-"`
}

var ErrVideoSubmissionConflict = errors.New("Idempotency-Key was used for a different request")
var ErrVideoSubmissionClaimLost = errors.New("submission cannot be sent; query its original receipt")

func ClaimVideoSubmission(candidate *VideoSubmission) (*VideoSubmission, bool, error) {
	candidate.OwnerToken = common.GetUUID()
	candidate.CreatedAt = time.Now().Unix()
	candidate.UpdatedAt = candidate.CreatedAt
	candidate.Deadline = candidate.CreatedAt + VideoSubmissionLeaseSeconds
	candidate.Status = VideoSubmissionPending
	result := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(candidate)
	if result.Error != nil {
		return nil, false, result.Error
	}
	record, err := GetVideoSubmission(candidate.UserID, candidate.ID)
	if err != nil {
		return nil, false, err
	}
	if record.OwnerToken == candidate.OwnerToken {
		return record, true, nil
	}
	if record.Fingerprint != candidate.Fingerprint {
		return record, false, ErrVideoSubmissionConflict
	}
	return record, false, nil
}

func GetVideoSubmission(userID int, id string) (*VideoSubmission, error) {
	var record VideoSubmission
	if err := DB.Where("id = ? AND user_id = ?", id, userID).First(&record).Error; err != nil {
		return nil, err
	}
	if !record.TaskReady && (record.Status == VideoSubmissionUnknown || record.Status == VideoSubmissionSubmitting || record.Status == VideoSubmissionAccepted) {
		if err := ReconcileVideoSubmission(userID, id); err != nil {
			return nil, err
		}
		if err := DB.Where("id = ? AND user_id = ?", id, userID).First(&record).Error; err != nil {
			return nil, err
		}
	}
	now := time.Now().Unix()
	// A crashed pre-send owner cannot later pass MarkVideoSubmissionSending.
	// A crashed post-send owner is always uncertain, never queued for resubmission.
	if record.Deadline <= now && (record.Status == VideoSubmissionPending || record.Status == VideoSubmissionSubmitting) {
		status := VideoSubmissionUnknown
		if record.Status == VideoSubmissionPending {
			status = VideoSubmissionNotAccepted
		}
		result := DB.Model(&VideoSubmission{}).Where("id = ? AND status = ? AND deadline <= ?", id, record.Status, now).Updates(map[string]any{"status": status, "updated_at": now})
		if result.Error != nil {
			return nil, result.Error
		}
		if err := DB.Where("id = ? AND user_id = ?", id, userID).First(&record).Error; err != nil {
			return nil, err
		}
	}
	return &record, nil
}

func MarkVideoSubmissionSending(id string, channelID int) error {
	now := time.Now().Unix()
	result := DB.Model(&VideoSubmission{}).Where("id = ? AND status = ? AND deadline > ?", id, VideoSubmissionPending, now).Updates(map[string]any{"status": VideoSubmissionSubmitting, "channel_id": channelID, "updated_at": now, "deadline": now + VideoSubmissionLeaseSeconds})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrVideoSubmissionClaimLost
	}
	return nil
}

func FinishVideoSubmission(id, status, upstreamID string, ready bool) error {
	allowed := []string{VideoSubmissionPending, VideoSubmissionSubmitting, VideoSubmissionUnknown}
	updates := map[string]any{"status": status, "updated_at": time.Now().Unix()}
	if upstreamID != "" {
		updates["upstream_task_id"] = upstreamID
	}
	if ready {
		updates["task_ready"] = true
	}
	// Accepted is monotonic; late failures may never overwrite it.
	if status == VideoSubmissionAccepted {
		allowed = append(allowed, VideoSubmissionAccepted)
	}
	result := DB.Model(&VideoSubmission{}).Where("id = ? AND status IN ?", id, allowed).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("submission finalization did not update its receipt")
	}
	return nil
}

// Create the task, its billing receipt and the ready marker in one transaction.
func InsertVideoSubmissionTask(task *Task, receipt *SeedanceBillingReceipt, submissionID string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(task).Error; err != nil {
			return err
		}
		if receipt != nil {
			if err := tx.Create(receipt).Error; err != nil {
				return err
			}
		}
		result := tx.Model(&VideoSubmission{}).Where("id = ? AND status IN ?", submissionID, []string{VideoSubmissionSubmitting, VideoSubmissionUnknown, VideoSubmissionAccepted}).Updates(map[string]any{"status": VideoSubmissionAccepted, "task_ready": true, "upstream_task_id": task.PrivateData.UpstreamTaskID, "updated_at": time.Now().Unix()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrVideoSubmissionClaimLost
		}
		return nil
	})
}

// Only an explicit rejection lets the same live owner cross the send boundary
// again. Uncertain, accepted, expired and replayed claims cannot be reset.
func RetryRejectedVideoSubmission(id, owner string, channel int) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var row VideoSubmission
		if err := tx.Where("id = ? AND owner_token = ? AND status = ? AND channel_id = ? AND deadline > ?", id, owner, VideoSubmissionSubmitting, channel, time.Now().Unix()).First(&row).Error; err != nil {
			return err
		}
		var attempts []map[string]any
		if row.AttemptsJSON != "" {
			if err := common.UnmarshalJsonStr(row.AttemptsJSON, &attempts); err != nil {
				return err
			}
		}
		attempts = append(attempts, map[string]any{"channel_id": channel, "status": "confirmed_not_accepted", "at": time.Now().Unix()})
		raw, err := common.Marshal(attempts)
		if err != nil {
			return err
		}
		res := tx.Model(&VideoSubmission{}).Where("id = ? AND owner_token = ? AND status = ? AND channel_id = ? AND updated_at = ?", id, owner, VideoSubmissionSubmitting, channel, row.UpdatedAt).Updates(map[string]any{"status": VideoSubmissionPending, "attempts_json": string(raw), "updated_at": time.Now().Unix()})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return ErrVideoSubmissionClaimLost
		}
		return nil
	})
}
