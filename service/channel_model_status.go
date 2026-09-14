package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"gorm.io/gorm"
)

func setAutomaticModelStatus(channelID int, modelName, reason string, enabled bool, source string, expectedVersions ...string) (bool, error) {
	return setAutomaticModelStatusWithTargets(channelID, modelName, reason, enabled, source, nil, expectedVersions...)
}

func setAutomaticModelStatusWithTargets(channelID int, modelName, reason string, enabled bool, source string, targets []types.ChannelProbeTarget, expectedVersions ...string) (bool, error) {
	reason = model.ChannelModelReasonSummary(reason)
	changed := false
	err := model.WithChannelOtherInfo(channelID, func(tx *gorm.DB, channel *model.Channel) error {
		if channel.Status != common.ChannelStatusEnabled {
			return nil
		}
		if _, manual := channel.GetManuallyDisabledModels()[modelName]; manual {
			return nil
		}
		info := channel.GetOtherInfo()
		entries := autoDisabledModelInfo(info)
		if enabled && entries[modelName] == nil {
			return nil
		}
		if enabled && len(expectedVersions) > 0 && channel.AutoDisabledModelVersion(modelName) != expectedVersions[0] {
			return nil
		}
		var mergedTargets []types.ChannelProbeTarget
		newTarget := false
		if !enabled {
			var err error
			mergedTargets, err = channel.AutoDisabledModelProbeTargets(modelName)
			if err != nil {
				return err
			}
			for _, target := range targets {
				if !target.Valid() || target.ModelName != modelName {
					continue
				}
				found := false
				for _, saved := range mergedTargets {
					found = found || saved == target
				}
				if !found {
					mergedTargets = append(mergedTargets, target)
					newTarget = true
				}
			}
		}
		result := tx.Model(&model.Ability{}).
			Where("channel_id = ? AND model = ? AND enabled = ?", channelID, modelName, !enabled).
			Update("enabled", enabled)
		if result.Error != nil || (result.RowsAffected == 0 && !newTarget) {
			return result.Error
		}
		action := "disable"
		if enabled {
			action = "enable"
		}
		event := model.ChannelModelEvent{ChannelID: channelID, Model: modelName, Action: action, Source: source, Reason: reason, CreatedAt: common.GetTimestamp()}
		if err := tx.Create(&event).Error; err != nil {
			return err
		}
		if enabled {
			delete(entries, modelName)
		} else {
			entry := newAutoDisabledModelEntry(common.GetTimestamp(), reason)
			entry["event_id"] = event.ID
			if len(mergedTargets) > 0 {
				entry["probe_targets"] = mergedTargets
			}
			entries[modelName] = entry
		}
		if len(entries) == 0 {
			delete(info, autoDisabledModelsInfoKey)
		} else {
			info[autoDisabledModelsInfoKey] = entries
		}
		channel.SetOtherInfo(info)
		changed = true
		return nil
	})
	return changed && err == nil, err
}
