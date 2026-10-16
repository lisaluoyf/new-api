package model

import (
	"context"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"regexp"
	"strings"
)

// VideoFeeWebhookInbox durably records authenticated supplier events before ACK.
// Event IDs remain available for deduplication for at least seven days.
type VideoFeeWebhookInbox struct {
	ID          string `gorm:"primaryKey;type:varchar(128)"`
	TaskID      int64  `gorm:"index"`
	Payload     string `gorm:"type:text"`
	Status      string `gorm:"type:varchar(16);index"`
	ReceivedAt  int64
	ProcessedAt int64
	NextAt      int64 `gorm:"index"`
	LeaseUntil  int64
	Attempts    int
}

// The provider may first return this placeholder and publish a detailed reason
// later. Match only the exact placeholder, including its sanitized public form.
var pendingTaskFailureReason = regexp.MustCompile(`^原因待核查(?:（任务 (?:[A-Za-z0-9_-]{1,191}|\[redacted\])）|\(任务 (?:[A-Za-z0-9_-]{1,191}|\[redacted\])\))?$`)

// Only generic failure text may be enriched. Financial state, finish time and
// customer terminal events remain unchanged, including on concurrent replay.
func IsGenericTaskFailureReason(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "", "video generation failed", "generation failed", "task failed":
		return true
	default:
		return pendingTaskFailureReason.MatchString(strings.TrimSpace(reason))
	}
}

func EnrichVideoFeeFailureReason(ctx context.Context, task *Task, reason string) (bool, error) {
	if task == nil || task.Status != TaskStatusFailure || task.ChannelId != constant.VideoFeeSeedanceChannelID ||
		task.Platform != constant.TaskPlatformVideoFee || !IsGenericTaskFailureReason(task.FailReason) || IsGenericTaskFailureReason(reason) {
		return false, nil
	}
	query := DB.WithContext(ctx).Model(&Task{}).
		Where("id = ? AND user_id = ? AND channel_id = ? AND platform = ? AND status = ?",
			task.ID, task.UserId, task.ChannelId, task.Platform, TaskStatusFailure)
	if task.FailReason == "" {
		query = query.Where("(fail_reason = ? OR fail_reason IS NULL)", "")
	} else {
		query = query.Where("fail_reason = ?", task.FailReason)
	}
	result := query.Updates(map[string]any{"fail_reason": strings.TrimSpace(reason), "updated_at": common.GetTimestamp()})
	return result.RowsAffected == 1, result.Error
}
