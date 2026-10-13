package service

import (
	"context"
	"fmt"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// RefreshBeeNexSeedance25Task only queries an already accepted channel-274 task.
// It never creates a generation and uses the normal terminal CAS/billing path.
func RefreshBeeNexSeedance25Task(ctx context.Context, task *model.Task) error {
	if task.ChannelId != constant.BeeNexSeedanceChannelID || task.Properties.OriginModelName != "seedance-2.5" || task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure {
		return nil
	}
	if GetTaskAdaptorFunc == nil {
		return fmt.Errorf("task query adapter unavailable")
	}
	a := GetTaskAdaptorFunc(constant.TaskPlatformBeeNex)
	if a == nil {
		return fmt.Errorf("task query adapter unavailable")
	}
	ch, err := model.GetChannelById(task.ChannelId, true)
	if err != nil {
		return fmt.Errorf("task query channel unavailable")
	}
	a.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: ch.GetBaseURL(), ApiKey: ch.Key}})
	if err := updateVideoSingleTask(ctx, a, ch, task.GetUpstreamTaskID(), map[string]*model.Task{task.GetUpstreamTaskID(): task}); err != nil {
		return err
	}
	return model.DB.WithContext(ctx).First(task, task.ID).Error
}
