package model

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// FreeModelMember stores routing policy which is intentionally independent from
// normal channel priority/weight and from channel_model_pricings.
type FreeModelMember struct {
	ChannelID         int    `json:"channel_id" gorm:"primaryKey;autoIncrement:false"`
	Enabled           bool   `json:"enabled" gorm:"not null"`
	Priority          int64  `json:"priority" gorm:"not null;index"`
	Weight            uint   `json:"weight" gorm:"not null"`
	Text              bool   `json:"text" gorm:"not null"`
	Vision            bool   `json:"vision" gorm:"not null"`
	Tools             bool   `json:"tools" gorm:"not null"`
	CodexTools        *bool  `json:"codex_tools,omitempty"`
	CodexPriority     *int64 `json:"codex_priority,omitempty" gorm:"index"`
	CodexWeight       *uint  `json:"codex_weight,omitempty"`
	RequiredToolCall  *bool  `json:"required_tool_call,omitempty"`
	JSONObject        bool   `json:"json_object" gorm:"not null"`
	JSONSchema        bool   `json:"json_schema" gorm:"not null"`
	ChatCompletions   *bool  `json:"chat_completions,omitempty"`
	Responses         *bool  `json:"responses,omitempty"`
	Messages          *bool  `json:"messages,omitempty"`
	MaxContextTokens  int    `json:"max_context_tokens" gorm:"not null"`
	TimeoutMS         int    `json:"timeout_ms" gorm:"not null"`
	DailyRequestLimit int    `json:"daily_request_limit" gorm:"not null;default:0"`
	CreatedAt         int64  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt         int64  `json:"updated_at" gorm:"autoUpdateTime"`
}

func boolPointer(value bool) *bool {
	return &value
}

func DefaultFreeModelMember(channelID int) FreeModelMember {
	return FreeModelMember{
		ChannelID: channelID, Enabled: true, Priority: 100, Weight: 100,
		Text: true, ChatCompletions: boolPointer(true), Responses: boolPointer(true), Messages: boolPointer(true),
		MaxContextTokens: 32768, TimeoutMS: 30000,
	}
}

func (m FreeModelMember) SupportsChatCompletions() bool {
	return m.ChatCompletions == nil || *m.ChatCompletions
}

func (m FreeModelMember) SupportsResponses() bool {
	return m.Responses == nil || *m.Responses
}

func (m FreeModelMember) SupportsMessages() bool {
	return m.Messages == nil || *m.Messages
}

func (m FreeModelMember) SupportsRequiredToolCall() bool {
	return m.RequiredToolCall == nil || *m.RequiredToolCall
}

func (m FreeModelMember) SupportsCodexTools() bool {
	if m.CodexTools == nil {
		return m.Tools
	}
	return *m.CodexTools
}

func (m FreeModelMember) CodexRouting() (int64, uint) {
	priority, weight := m.Priority, m.Weight
	if m.CodexPriority != nil {
		priority = *m.CodexPriority
	}
	if m.CodexWeight != nil {
		weight = *m.CodexWeight
	}
	return priority, weight
}

func GetFreeModelMember(channelID int) (FreeModelMember, bool, error) {
	member := DefaultFreeModelMember(channelID)
	var stored FreeModelMember
	err := DB.First(&stored, "channel_id = ?", channelID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return member, false, nil
	}
	if err != nil {
		return member, false, err
	}
	return stored, true, nil
}

func GetFreeModelMembers(channelIDs []int) (map[int]FreeModelMember, error) {
	result := make(map[int]FreeModelMember, len(channelIDs))
	for _, channelID := range channelIDs {
		result[channelID] = DefaultFreeModelMember(channelID)
	}
	if len(channelIDs) == 0 {
		return result, nil
	}
	var rows []FreeModelMember
	if err := DB.Where("channel_id IN ?", channelIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.ChannelID] = row
	}
	return result, nil
}

func UpsertFreeModelMember(member FreeModelMember) error {
	return DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "channel_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"enabled", "priority", "weight", "text", "vision", "tools", "codex_tools", "codex_priority", "codex_weight", "required_tool_call", "json_object", "json_schema", "chat_completions", "responses", "messages", "max_context_tokens", "timeout_ms", "daily_request_limit", "updated_at"}),
	}).Create(&member).Error
}
