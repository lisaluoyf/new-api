package service

import (
	"context"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"time"
)

// Notification subscribers must progress even if the customer never polls.
func UpdateWebhookImageTasks(tasks map[string]*model.Task) {
	for _, task := range tasks {
		if task.PrivateData.Webhook == nil {
			continue
		}
		ch, err := model.GetChannelById(task.ChannelId, true)
		if err != nil {
			continue
		}
		key := task.PrivateData.Key
		if key == "" {
			selected, _, e := ch.GetNextEnabledKey()
			if e != nil {
				continue
			}
			key = selected
		}
		targets := []ImageTaskTarget{{ChannelID: ch.Id, BaseURL: ch.GetBaseURL(), APIKey: key, TaskID: task.GetUpstreamTaskID()}}
		keys := map[int]string{ch.Id: key}
		if task.PrivateData.HedgeChannelId != 0 && task.PrivateData.HedgeUpstreamTaskID != "" {
			if hedge, e := model.GetChannelById(task.PrivateData.HedgeChannelId, true); e == nil {
				if k, _, e := hedge.GetNextEnabledKey(); e == nil {
					targets = append(targets, ImageTaskTarget{ChannelID: hedge.Id, BaseURL: hedge.GetBaseURL(), APIKey: k, TaskID: task.PrivateData.HedgeUpstreamTaskID})
					keys[hedge.Id] = k
				}
			}
		}
		winner, status, url, reason, _ := CheckImageTaskTargetsOnce(targets)
		old := task.Status
		switch status {
		case "completed", "succeeded", "success":
			headers := map[string]string{"Authorization": "Bearer " + keys[winner.ChannelID]}
			task.PrivateData.ResultURL = CacheImageLocallyWithHeaders(url, headers)
			task.PrivateData.ImageResultURLs = nil
			for _, u := range winner.ImageURLs {
				task.PrivateData.ImageResultURLs = append(task.PrivateData.ImageResultURLs, CacheImageLocallyWithHeaders(u, headers))
			}
			if winner.ChannelID != 0 {
				task.ChannelId = winner.ChannelID
				task.PrivateData.UpstreamTaskID = winner.TaskID
			}
			task.Status = model.TaskStatusSuccess
		case "failed", "error", "cancelled":
			task.Status = model.TaskStatusFailure
			task.FailReason = reason
		default:
			continue
		}
		task.Progress = "100%"
		task.FinishTime = time.Now().Unix()
		won, err := task.UpdateWithStatus(old)
		if err != nil {
			common.SysError("image webhook terminal persistence failed: " + err.Error())
			continue
		}
		if !won {
			continue
		}
		if task.Status == model.TaskStatusFailure {
			RefundImageAsyncTaskQuota(context.Background(), task, task.FailReason)
		}
	}
}
