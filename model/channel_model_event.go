package model

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Events are append-only; recovery must not erase the reason for a past disable.
type ChannelModelEvent struct {
	ID        int64  `json:"id" gorm:"primaryKey"`
	ChannelID int    `json:"channel_id" gorm:"index:idx_channel_model_event,priority:1"`
	Model     string `json:"model" gorm:"type:varchar(255);index:idx_channel_model_event,priority:2"`
	Action    string `json:"action" gorm:"type:varchar(32)"`
	Source    string `json:"source" gorm:"type:varchar(32)"`
	ActorID   int    `json:"actor_id"`
	Reason    string `json:"reason" gorm:"type:text"`
	CreatedAt int64  `json:"created_at"`
}

// Serialize read-modify-write across workers and traffic replicas. Callers must
// use the freshly locked channel, never a snapshot obtained before a probe.
func WithChannelOtherInfo(channelID int, fn func(*gorm.DB, *Channel) error) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var channel Channel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&channel, "id = ?", channelID).Error; err != nil {
			return err
		}
		before := channel.OtherInfo
		if err := fn(tx, &channel); err != nil {
			return err
		}
		if channel.OtherInfo == before {
			return nil
		}
		return tx.Model(&Channel{}).Where("id = ?", channelID).Update("other_info", channel.OtherInfo).Error
	})
}

func RecordChannelModelEvent(tx *gorm.DB, channelID int, model, action, source, reason string, actorID int) error {
	return tx.Create(&ChannelModelEvent{
		ChannelID: channelID, Model: model, Action: action, Source: source,
		Reason: ChannelModelReasonSummary(reason), ActorID: actorID, CreatedAt: common.GetTimestamp(),
	}).Error
}

func ChannelModelReasonSummary(reason string) string {
	// Preserve the diagnostic message without retaining upstream response bodies.
	reason, _, _ = strings.Cut(reason, ", body:")
	runes := []rune(common.MaskSensitiveInfo(strings.TrimSpace(reason)))
	if len(runes) > 2048 {
		return string(runes[:2048]) + "..."
	}
	return string(runes)
}

// Automatic model disables must survive rebuilding the derived abilities too.
func (channel *Channel) GetDisabledModels() map[string]struct{} {
	disabled := channel.GetManuallyDisabledModels()
	if entries, ok := channel.GetOtherInfo()["auto_disabled_models"].(map[string]interface{}); ok {
		for name := range entries {
			disabled[name] = struct{}{}
		}
	}
	return disabled
}

func (channel *Channel) AutoDisabledModelVersion(modelName string) string {
	entries, _ := channel.GetOtherInfo()["auto_disabled_models"].(map[string]interface{})
	entry, _ := entries[modelName].(map[string]interface{})
	if entry == nil {
		return ""
	}
	if id := entry["event_id"]; id != nil {
		return fmt.Sprint("event/", id)
	}
	return fmt.Sprint("legacy/", entry["disabled_at"])
}
