package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

func setAutomaticModelStatus(channelID int, modelName, reason string, enabled bool, source string, expectedVersions ...string) (bool, error) {
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
		result := tx.Model(&model.Ability{}).
			Where("channel_id = ? AND model = ? AND enabled = ?", channelID, modelName, !enabled).
			Update("enabled", enabled)
		if result.Error != nil || result.RowsAffected == 0 {
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
