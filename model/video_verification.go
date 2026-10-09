package model

import (
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type VideoFingerprintCheck struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Value    string `json:"value"`
	Expected string `json:"expected,omitempty"`
}

var VideoFingerprintCheckIDs = []string{"claim", "dimensions", "family", "x264", "frames"}

func PublicVideoFingerprintChecks(raw string) []VideoFingerprintCheck {
	var checks []VideoFingerprintCheck
	if common.Unmarshal([]byte(raw), &checks) != nil || len(checks) != len(VideoFingerprintCheckIDs) {
		return nil
	}
	byID := make(map[string]VideoFingerprintCheck)
	for _, check := range checks {
		if _, duplicate := byID[check.ID]; duplicate || check.Status != "pass" || strings.TrimSpace(check.Value) == "" || utf8.RuneCountInString(check.Value) > 256 {
			return nil
		}
		byID[check.ID] = check
	}
	result := make([]VideoFingerprintCheck, 0, len(checks))
	for _, id := range VideoFingerprintCheckIDs {
		check, exists := byID[id]
		if !exists {
			return nil
		}
		result = append(result, check)
	}
	return result
}

func NormalizeVerifiedVideoModel(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "seedance-2.0", "doubao-seedance-2.0":
		return "seedance-2.0"
	case "seedance-2.0-fast", "doubao-seedance-2.0-fast":
		return "seedance-2.0-fast"
	case "seedance-2.0-mini", "doubao-seedance-2.0-mini":
		return "seedance-2.0-mini"
	case "seedance-2.5", "doubao-seedance-2.5":
		return "seedance-2.5"
	default:
		name = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(name)), "doubao-")
		if regexp.MustCompile(`^seedance-[0-9]+\.[0-9]+(-[a-z0-9]+)*$`).MatchString(name) {
			return name
		}
		return ""
	}
}

func NormalizeAutomaticVerifiedVideoModel(name string) string {
	normalized := NormalizeVerifiedVideoModel(name)
	if normalized == "seedance-2.0" || normalized == "seedance-2.5" {
		return normalized
	}
	for _, enabled := range strings.Split(os.Getenv("SEEDANCE_AUTO_VERIFIED_MODELS"), ",") {
		if normalized != "" && normalized == strings.TrimSpace(enabled) {
			return normalized
		}
	}
	return ""
}

type VideoVerificationState struct {
	ID             int64  `gorm:"primaryKey"`
	ChannelID      int    `gorm:"uniqueIndex:video_verification_route"`
	Model          string `gorm:"type:varchar(64);uniqueIndex:video_verification_route"`
	LastTaskID     int64
	ActiveTaskID   int64
	LeaseUntil     int64
	NextAt         int64
	LastStatus     string `gorm:"type:varchar(32)"`
	LastDetectedAt int64
}

type VideoVerificationRun struct {
	FingerprintModelVersion string `gorm:"type:varchar(128)"`
	DetectorVersion         string `gorm:"type:varchar(128)"`
	BaselineSHA256          string `gorm:"type:text"`
	ID                      int64  `gorm:"primaryKey"`
	TaskID                  int64  `gorm:"uniqueIndex"`
	ChannelID               int
	Model                   string `gorm:"type:varchar(64)"`
	CompletedAt             int64
	Status                  string `gorm:"type:varchar(16);index"`
	LeaseUntil              int64  `gorm:"index"`
}

type VideoVerificationAlert struct {
	FingerprintModelVersion string `gorm:"type:varchar(128)"`
	DetectorVersion         string `gorm:"type:varchar(128)"`
	BaselineSHA256          string `gorm:"type:text"`
	ID                      int64  `gorm:"primaryKey"`
	TaskID                  int64  `gorm:"uniqueIndex"`
	ChannelID               int
	Model                   string `gorm:"type:varchar(64)"`
	Reason                  string `gorm:"type:varchar(64)"`
	VideoChecksJSON         string `gorm:"type:text"`
	DetectedAt              int64
	Attempts                int
	NextAt                  int64 `gorm:"index"`
	LeaseUntil              int64
	Status                  string `gorm:"type:varchar(16);index"`
}

func (task *Task) enqueueVideoVerification(tx *gorm.DB) error {
	name := NormalizeAutomaticVerifiedVideoModel(task.Properties.OriginModelName)
	if task.Status != TaskStatusSuccess || task.ChannelId <= 0 || name == "" {
		return nil
	}
	run := VideoVerificationRun{TaskID: task.ID, ChannelID: task.ChannelId, Model: name, CompletedAt: time.Now().Unix(), Status: "pending"}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&run).Error
}
