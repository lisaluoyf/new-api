package service

import (
	"context"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"golang.org/x/sync/singleflight"
)

var beeNexTaskRefreshes singleflight.Group

// Concurrent owner polls share one live refresh. Only confirmed terminal DB
// records can satisfy a failed query; cached pending progress is never live data.
func RefreshBeeNexSeedanceTask(ctx context.Context, task *model.Task) error {
	if task == nil || task.ChannelId != constant.BeeNexSeedanceChannelID || task.Properties.OriginModelName != "seedance-2.0" && task.Properties.OriginModelName != "seedance-2.5" || task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure {
		return nil
	}
	if task.ID <= 0 {
		return &BeeNexTaskQueryError{Kind: "invalid_context"}
	}
	key := fmt.Sprintf("%d:%d:%s", task.UserId, task.ID, task.TaskID)
	result := beeNexTaskRefreshes.DoChan(key, func() (any, error) {
		// A canceled polling client must not cancel another owner poll sharing
		// this read. Accepted-task settlement remains bounded and CAS protected.
		work, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		load := func() (*model.Task, error) {
			var current model.Task
			err := model.DB.WithContext(work).Where("id = ? AND user_id = ? AND channel_id = ?", task.ID, task.UserId, task.ChannelId).First(&current).Error
			return &current, err
		}
		current, err := load()
		if err != nil {
			return nil, err
		}
		if current.Status == model.TaskStatusSuccess || current.Status == model.TaskStatusFailure {
			return current, nil
		}
		if GetTaskAdaptorFunc == nil {
			return nil, fmt.Errorf("task query adapter unavailable")
		}
		a := GetTaskAdaptorFunc(constant.TaskPlatformBeeNex)
		if a == nil {
			return nil, fmt.Errorf("task query adapter unavailable")
		}
		ch, err := model.GetChannelById(current.ChannelId, true)
		if err != nil {
			return nil, fmt.Errorf("task query channel unavailable")
		}
		a.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: ch.GetBaseURL(), ApiKey: ch.Key}})
		if err := updateVideoSingleTask(work, a, ch, current.GetUpstreamTaskID(), map[string]*model.Task{current.GetUpstreamTaskID(): current}); err != nil {
			if saved, lookup := load(); lookup == nil && (saved.Status == model.TaskStatusSuccess || saved.Status == model.TaskStatusFailure) {
				return saved, nil // A worker concurrently confirmed the terminal state.
			}
			return nil, err
		}
		return load()
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case done := <-result:
		if done.Err != nil {
			return done.Err
		}
		*task = *done.Val.(*model.Task)
		return nil
	}
}
