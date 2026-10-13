package service

import (
	"context"
	"errors"
	"fmt"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

type BeeNexTaskQueryError struct {
	Protocol   string
	HTTPStatus int
}

func (e *BeeNexTaskQueryError) Error() string {
	return fmt.Sprintf("BeeNex task query failed: HTTP %d; expected JSON", e.HTTPStatus)
}

// Logs contain only locally defined fields, never provider bodies or media URLs.
func BeeNexTaskQueryFailureSummary(err error) string {
	var queryError *BeeNexTaskQueryError
	if errors.As(err, &queryError) {
		return fmt.Sprintf("protocol=%s http=%d", queryError.Protocol, queryError.HTTPStatus)
	}
	return "query_or_state_update_failed"
}

// Defaults retain the deployed protocol; an explicit per-model setting takes priority.
func BeeNexTaskQueryProtocol(ch *model.Channel, name string) (string, error) {
	if ch == nil || ch.Id != constant.BeeNexSeedanceChannelID || !IsBeeNexSeedanceModel(name) {
		return "", fmt.Errorf("unsupported BeeNex query context")
	}
	protocol := ch.GetSetting().BeeNexQueryProtocols[name]
	if protocol == "" {
		protocol = "video_generations"
		if name == "seedance-2.5" {
			protocol = "sora"
		}
	}
	if protocol != "sora" && protocol != "video_generations" {
		return "", fmt.Errorf("invalid BeeNex task query protocol configuration")
	}
	return protocol, nil
}

// RefreshBeeNexSeedanceTask only queries an already accepted channel-274 task.
// It never creates a generation and uses the normal terminal CAS/billing path.
func RefreshBeeNexSeedanceTask(ctx context.Context, task *model.Task) error {
	if task.ChannelId != constant.BeeNexSeedanceChannelID || !IsBeeNexSeedanceModel(task.Properties.OriginModelName) || task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure {
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
