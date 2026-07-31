package model

import "github.com/QuantumNous/new-api/common"

// RegistryAccessLog records Kimi Code custom-registry synchronization requests.
// It lives in LOG_DB so deployments with a dedicated log database keep these
// operational records out of the primary transactional database.
type RegistryAccessLog struct {
	Id         int    `json:"id" gorm:"index:idx_registry_created_id,priority:2"`
	UserId     int    `json:"user_id" gorm:"index"`
	TokenId    int    `json:"token_id" gorm:"index"`
	CreatedAt  int64  `json:"created_at" gorm:"bigint;index:idx_registry_created_id,priority:1"`
	ModelCount int    `json:"model_count"`
	UserAgent  string `json:"user_agent" gorm:"type:varchar(512)"`
}

func RecordRegistryAccess(userID int, tokenID int, modelCount int, userAgent string) error {
	if len(userAgent) > 512 {
		userAgent = userAgent[:512]
	}
	return LOG_DB.Create(&RegistryAccessLog{
		UserId:     userID,
		TokenId:    tokenID,
		CreatedAt:  common.GetTimestamp(),
		ModelCount: modelCount,
		UserAgent:  userAgent,
	}).Error
}

func GetRegistryAccessLogs(offset int, limit int) ([]*RegistryAccessLog, int64, error) {
	var logs []*RegistryAccessLog
	var total int64
	query := LOG_DB.Model(&RegistryAccessLog{})
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := query.Order("id DESC").Offset(offset).Limit(limit).Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	return logs, total, nil
}
