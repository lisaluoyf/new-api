package model

import "gorm.io/gorm"

// SeedanceResource separates each customer's media library from the shared
// provider account. Provider identifiers and credentials never enter a DTO.
type SeedanceResource struct {
	MeasuredDurationSeconds float64        `json:"-" gorm:"default:0"`
	DurationSeconds         int            `json:"-" gorm:"default:0"`
	ID                      string         `json:"id" gorm:"primaryKey;size:64"`
	UserID                  int            `json:"-" gorm:"index:idx_seedance_owner_kind"`
	Kind                    string         `json:"-" gorm:"index:idx_seedance_owner_kind;size:16"`
	ChannelID               int            `json:"-"`
	KeyFingerprint          string         `json:"-" gorm:"size:64"`
	UpstreamID              string         `json:"-" gorm:"size:128;index"`
	Model                   string         `json:"model,omitempty" gorm:"size:64"`
	Name                    string         `json:"name,omitempty" gorm:"size:255"`
	Description             string         `json:"description,omitempty" gorm:"type:text"`
	AssetType               string         `json:"asset_type,omitempty" gorm:"size:16"`
	GroupID                 string         `json:"group_id,omitempty" gorm:"index;size:64"`
	SourceURL               string         `json:"url,omitempty" gorm:"type:text"`
	Status                  string         `json:"status" gorm:"size:32"`
	Progress                int            `json:"progress"`
	RequestData             string         `json:"-" gorm:"type:text"`
	ResultData              string         `json:"-" gorm:"type:text"`
	FailReason              string         `json:"-" gorm:"type:text"`
	CreatedAt               int64          `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt               int64          `json:"updated_at" gorm:"autoUpdateTime"`
	DeletedAt               gorm.DeletedAt `json:"-" gorm:"index"`
}

func GetSeedanceResource(userID int, kind, id string) (*SeedanceResource, error) {
	var resource SeedanceResource
	err := DB.Where("user_id = ? AND kind = ? AND id = ?", userID, kind, id).First(&resource).Error
	return &resource, err
}

func IsSeedancePrivateReviewTask(id string) (bool, error) {
	if DB == nil {
		return false, nil
	}
	var count int64
	err := DB.Model(&SeedanceResource{}).Where("kind = ? AND upstream_id = ?", "task", id).Count(&count).Error
	return count > 0, err
}
