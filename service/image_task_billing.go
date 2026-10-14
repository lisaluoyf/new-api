package service

import (
	"context"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
)

// imageTaskTerminalFailure reports whether upstream returned a terminal failure.
func imageTaskTerminalFailure(status string) bool {
	switch status {
	case "failed", "error", "cancelled":
		return true
	default:
		return false
	}
}

// upstreamImageTaskConfirmedUncharged re-polls upstream once and requires a terminal
// failure with zero reported cost before we refund the user.
func upstreamImageTaskConfirmedUncharged(task *model.Task) (bool, ImageTaskPollResult) {
	if task == nil {
		return false, ImageTaskPollResult{}
	}
	upstreamID := strings.TrimSpace(task.GetUpstreamTaskID())
	if upstreamID == "" || task.ChannelId <= 0 {
		return true, ImageTaskPollResult{}
	}
	channel, err := model.GetChannelById(task.ChannelId, true)
	if err != nil || channel == nil {
		return false, ImageTaskPollResult{}
	}
	key, _, apiErr := channel.GetNextEnabledKey()
	if apiErr != nil {
		return false, ImageTaskPollResult{}
	}
	poll, err := fetchImageTaskStatusOnce(channel.GetBaseURL(), key, upstreamID)
	if err != nil {
		return false, poll
	}
	if !imageTaskTerminalFailure(poll.Status) {
		return false, poll
	}
	if poll.UpstreamCost > 0 || poll.CreditsCost > 0 {
		return false, poll
	}
	return true, poll
}

func intFromLogOther(other map[string]interface{}, key string) int {
	if other == nil {
		return 0
	}
	switch v := other[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case string:
		return common.String2Int(v)
	default:
		return 0
	}
}

// RefundImageAsyncTaskQuota refunds the submit-time consume log for a tracked gpt-image-2
// async task that finished in FAILURE. Billing happens at submit (PostTextConsumeQuota),
// but the Task row historically kept quota=0, so RefundTaskQuota was never triggered.
func RefundImageAsyncTaskQuota(ctx context.Context, task *model.Task, reason string) {
	if task == nil || task.UserId <= 0 || strings.TrimSpace(task.TaskID) == "" {
		return
	}
	if task.Status != model.TaskStatusFailure {
		return
	}
	if item, err := model.GetImageTaskRefund(task.UserId, task.TaskID); err == nil {
		if item.Mode == "refund" {
			if _, err := model.ApplyImageTaskRefund(item.ID); err != nil {
				logger.LogWarn(ctx, "image refund replay failed: "+err.Error())
			}
		}
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		logger.LogWarn(ctx, "image refund receipt lookup failed: "+err.Error())
		return
	}
	if refunded, err := model.HasRefundLogForTask(task.UserId, task.TaskID); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("refund lookup failed for task %s: %v", task.TaskID, err))
		return
	} else if refunded {
		return
	}

	ok, poll := upstreamImageTaskConfirmedUncharged(task)
	if !ok {
		if poll.Status != "" {
			logger.LogWarn(ctx, fmt.Sprintf(
				"skip refund for task %s: upstream status=%s cost=%v credits_cost=%v",
				task.TaskID, poll.Status, poll.UpstreamCost, poll.CreditsCost,
			))
		}
		return
	}

	row, err := model.FindConsumeLogRowForTask(task.UserId, task.TaskID)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("consume log not found for task %s: %v", task.TaskID, err))
		return
	}
	quota := row.Quota
	if quota <= 0 {
		return
	}

	otherMap, _ := common.StrToMap(row.Other)
	billingSource := BillingSourceWallet
	if otherMap != nil {
		if src, ok := otherMap["billing_source"].(string); ok && strings.TrimSpace(src) != "" {
			billingSource = strings.TrimSpace(src)
		}
	}

	refundOther := map[string]interface{}{
		"task_id":       task.TaskID,
		"reason":        reason,
		"refund_source": "image_async_task_failure",
	}
	if poll.FailCode != "" {
		refundOther["task_fail_code"] = poll.FailCode
	}
	if strings.TrimSpace(poll.DisplayFailReason()) != "" {
		refundOther["task_fail_reason"] = poll.DisplayFailReason()
	}
	if otherMap != nil {
		for _, key := range []string{"model_price", "model_ratio", "group_ratio", "billing_source", "upstream_model_name", "is_model_mapped"} {
			if v, ok := otherMap[key]; ok {
				refundOther[key] = v
			}
		}
	}

	refund := &model.Log{UserId: row.UserId, Username: row.Username, CreatedAt: common.GetTimestamp(), Type: model.LogTypeRefund,
		Content: reason, TokenId: row.TokenId, TokenName: row.TokenName, ModelName: row.ModelName, Quota: quota,
		ChannelId: row.ChannelId, Group: row.Group, Other: common.MapToJsonStr(refundOther)}
	item, err := model.FreezeImageTaskRefund(task, row, refund, billingSource, intFromLogOther(otherMap, "subscription_id"))
	if err != nil {
		logger.LogWarn(ctx, "image refund freeze failed: "+err.Error())
		return
	}
	if _, err := model.ApplyImageTaskRefund(item.ID); err != nil {
		logger.LogWarn(ctx, "image refund apply/publication pending: "+err.Error())
		return
	}
	logger.LogInfo(ctx, fmt.Sprintf("refunded image async task %s user=%d quota=%s", task.TaskID, task.UserId, logger.LogQuota(quota)))
}

var imageRefundWorkerOnce sync.Once

func StartImageTaskRefundWorker() {
	if !common.IsMasterNode {
		return
	}
	imageRefundWorkerOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(20 * time.Second)
			defer ticker.Stop()
			for {
				if err := model.RetryImageTaskRefunds(); err != nil {
					common.SysError("image refund recovery: " + err.Error())
				}
				<-ticker.C
			}
		}()
	})
}
