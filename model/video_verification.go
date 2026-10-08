package model

import (
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
)

type VideoFingerprintCheck struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Value  string `json:"value"`
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
	case "seedance-2.5":
		return "seedance-2.5"
	default:
		return ""
	}
}

type VideoVerificationEvent struct {
	TaskID      int64
	CompletedAt int64
}

var videoVerificationSink struct {
	sync.RWMutex
	queue chan<- VideoVerificationEvent
}

func SetVideoVerificationQueue(queue chan<- VideoVerificationEvent) {
	videoVerificationSink.Lock()
	defer videoVerificationSink.Unlock()
	videoVerificationSink.queue = queue
}

func notifyVideoVerification(task *Task) {
	if task.Status != TaskStatusSuccess || task.ChannelId <= 0 || NormalizeVerifiedVideoModel(task.Properties.OriginModelName) == "" {
		return
	}
	videoVerificationSink.RLock()
	defer videoVerificationSink.RUnlock()
	if videoVerificationSink.queue == nil {
		return
	}
	select {
	case videoVerificationSink.queue <- VideoVerificationEvent{TaskID: task.ID, CompletedAt: time.Now().Unix()}:
	default:
		common.SysError("video verification event dropped: queue full")
	}
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
	ID          int64 `gorm:"primaryKey"`
	TaskID      int64 `gorm:"uniqueIndex"`
	ChannelID   int
	Model       string `gorm:"type:varchar(64)"`
	CompletedAt int64
	Status      string `gorm:"type:varchar(16);index"`
	LeaseUntil  int64
}

type VideoVerificationAlert struct {
	ID         int64 `gorm:"primaryKey"`
	TaskID     int64 `gorm:"uniqueIndex"`
	ChannelID  int
	Model      string `gorm:"type:varchar(64)"`
	Reason     string `gorm:"type:varchar(64)"`
	DetectedAt int64
	Attempts   int
	NextAt     int64 `gorm:"index"`
	LeaseUntil int64
	Status     string `gorm:"type:varchar(16);index"`
}
