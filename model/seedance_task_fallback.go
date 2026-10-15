package model

import "github.com/QuantumNous/new-api/dto"

// Private recovery context. Never returned in task DTOs or written to logs.
// Public material IDs are retained; provider-specific asset IDs are rebuilt.
type SeedanceFallbackRequest struct {
	Fields     map[string]any       `json:"fields"`
	TokenGroup string               `json:"token_group"`
	UserGroup  string               `json:"user_group"`
	ClientType string               `json:"client_type"`
	Policy     *dto.ProviderSetting `json:"policy,omitempty"`
	Started    bool                 `json:"started,omitempty"`
	Stage      string               `json:"stage,omitempty"`
}

// One recovery attempt per public task. State is committed before remote POST;
// a crashed/ambiguous sender is never allowed to POST again.
type SeedanceTaskFallback struct {
	TaskID             int64  `gorm:"primaryKey;autoIncrement:false"`
	State              string `gorm:"size:32;index:idx_sd_fallback_work,priority:1"`
	TargetChannelID    int
	TargetUnitCost     float64
	OriginalChannelID  int
	OriginalUpstreamID string `gorm:"size:191"`
	OriginalReason     string `gorm:"type:text"`
	UpstreamID         string `gorm:"size:191"`
	KeyFingerprint     string `gorm:"size:64"`
	LeaseToken         string `gorm:"size:64"`
	LeaseUntil         int64  `gorm:"index:idx_sd_fallback_work,priority:2"`
	Deadline           int64
	CreatedAt          int64
	UpdatedAt          int64
}
