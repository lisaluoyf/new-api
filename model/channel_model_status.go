package model

import (
	"errors"
	"sort"
	"strings"

	"gorm.io/gorm"
)

const manuallyDisabledModelsInfoKey = "manually_disabled_models"

// GetManuallyDisabledModels returns the exact model names that an administrator
// disabled from the channel-data page. The state lives in channels.other_info so
// rebuilding the derived abilities table cannot silently lose the operator's
// choice.
func (channel *Channel) GetManuallyDisabledModels() map[string]struct{} {
	disabled := make(map[string]struct{})
	if channel == nil {
		return disabled
	}

	raw := channel.GetOtherInfo()[manuallyDisabledModelsInfoKey]
	switch values := raw.(type) {
	case []interface{}:
		for _, value := range values {
			if name, ok := value.(string); ok {
				name = strings.TrimSpace(name)
				if name != "" {
					disabled[name] = struct{}{}
				}
			}
		}
	case []string:
		for _, name := range values {
			name = strings.TrimSpace(name)
			if name != "" {
				disabled[name] = struct{}{}
			}
		}
	case map[string]interface{}:
		// Accept the object form too, in case an older/manual record used it.
		for name := range values {
			name = strings.TrimSpace(name)
			if name != "" {
				disabled[name] = struct{}{}
			}
		}
	}
	return disabled
}

func (channel *Channel) setManuallyDisabledModels(disabled map[string]struct{}) {
	info := channel.GetOtherInfo()
	if len(disabled) == 0 {
		delete(info, manuallyDisabledModelsInfoKey)
		channel.SetOtherInfo(info)
		return
	}

	modelNames := make([]string, 0, len(disabled))
	for name := range disabled {
		modelNames = append(modelNames, name)
	}
	sort.Strings(modelNames)
	info[manuallyDisabledModelsInfoKey] = modelNames
	channel.SetOtherInfo(info)
}

func (channel *Channel) clearAutoDisabledModels(modelNames []string) {
	info := channel.GetOtherInfo()
	raw, ok := info["auto_disabled_models"].(map[string]interface{})
	if !ok {
		return
	}
	for _, name := range modelNames {
		delete(raw, name)
	}
	if len(raw) == 0 {
		delete(info, "auto_disabled_models")
	} else {
		info["auto_disabled_models"] = raw
	}
	channel.SetOtherInfo(info)
}

// SetChannelModelsManuallyDisabled atomically updates both the routing abilities
// and their persistent source-of-truth metadata.
func SetChannelModelsManuallyDisabled(channelID int, modelNames []string, disabled bool) (int64, error) {
	if channelID <= 0 || len(modelNames) == 0 {
		return 0, errors.New("channel and model are required")
	}

	tx := DB.Begin()
	if tx.Error != nil {
		return 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()

	var channel Channel
	if err := tx.First(&channel, "id = ?", channelID).Error; err != nil {
		tx.Rollback()
		return 0, err
	}

	normalized := make([]string, 0, len(modelNames))
	seen := make(map[string]struct{}, len(modelNames))
	for _, name := range modelNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		normalized = append(normalized, name)
	}
	if len(normalized) == 0 {
		tx.Rollback()
		return 0, errors.New("model is required")
	}

	result := tx.Model(&Ability{}).
		Where("channel_id = ? AND model IN ?", channelID, normalized).
		Select("enabled").
		Update("enabled", !disabled)
	if result.Error != nil {
		tx.Rollback()
		return 0, result.Error
	}
	if result.RowsAffected == 0 {
		tx.Rollback()
		return 0, gorm.ErrRecordNotFound
	}

	persisted := channel.GetManuallyDisabledModels()
	for _, name := range normalized {
		if disabled {
			persisted[name] = struct{}{}
		} else {
			delete(persisted, name)
		}
	}
	channel.setManuallyDisabledModels(persisted)
	// An explicit operator action supersedes automatic disable/recovery state.
	// Otherwise a later fingerprint recovery could undo a manual disable.
	channel.clearAutoDisabledModels(normalized)
	if err := tx.Model(&Channel{}).
		Where("id = ?", channelID).
		Update("other_info", channel.OtherInfo).Error; err != nil {
		tx.Rollback()
		return 0, err
	}

	if err := tx.Commit().Error; err != nil {
		return 0, err
	}
	return result.RowsAffected, nil
}
