package model

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm/clause"
)

// CancellationObservation is an audit work item, never a debit authorization.
// In particular, missing supplier usage/cost is unknown, not zero. This table
// deliberately has no estimated charge or confirmed-charge operation.
type CancellationObservation struct {
	Id                    int    `json:"id" gorm:"primaryKey"`
	RequestId             string `json:"request_id" gorm:"type:varchar(64);uniqueIndex;not null"`
	UserId                int    `json:"user_id" gorm:"index"`
	TokenId               int    `json:"token_id"`
	ChannelId             int    `json:"channel_id" gorm:"index"`
	ModelName             string `json:"model_name" gorm:"type:varchar(255);index"`
	Source                string `json:"source" gorm:"type:varchar(32)"`
	CreatedAt             int64  `json:"created_at" gorm:"index"`
	StartedAt             int64  `json:"started_at"`
	DurationMS            int64  `json:"duration_ms"`
	IsStream              bool   `json:"is_stream"`
	ReceivedResponses     int    `json:"received_responses"`
	SentResponses         int    `json:"sent_responses"`
	UpstreamRequestId     string `json:"upstream_request_id" gorm:"type:varchar(255)"`
	UpstreamResponseId    string `json:"upstream_response_id" gorm:"type:varchar(255)"`
	CredentialFingerprint string `json:"credential_fingerprint" gorm:"type:varchar(64)"`
	RequestSnapshot       string `json:"request_snapshot" gorm:"type:text"`
	LocalState            string `json:"local_state" gorm:"type:varchar(32);index"`
	LocalEvidence         string `json:"local_evidence" gorm:"type:text"`
	ReviewReason          string `json:"review_reason" gorm:"type:varchar(64)"`
	LastCheckedAt         int64  `json:"last_checked_at"`
	Revision              int64  `json:"revision"`
	NextCheckAt           int64  `json:"next_check_at" gorm:"index"`
}

// The cursor cycles over the last day's error logs, so delayed log inserts and
// failed live enqueues are retried. Logs may live in a separate LOG_DB.
type CancellationObservationCursor struct {
	Name    string `gorm:"type:varchar(64);primaryKey"`
	AfterId int
	Since   int64
}

func CreateCancellationObservation(item *CancellationObservation) error {
	if item == nil || strings.TrimSpace(item.RequestId) == "" || len(item.RequestId) > 64 || item.UserId <= 0 || item.ChannelId <= 0 {
		return errors.New("invalid cancellation observation identity")
	}
	if item.CreatedAt == 0 {
		item.CreatedAt = common.GetTimestamp()
	}
	item.LocalState = "unchecked"
	item.ReviewReason = "supplier_evidence_not_collected"
	item.NextCheckAt = item.CreatedAt
	// Replays/backfill must not replace the original user, price snapshot,
	// credential identity or the worker's progress with a later guess.
	return DB.Clauses(clause.OnConflict{DoNothing: true}).Create(item).Error
}

func GetCancellationObservation(requestID string) (*CancellationObservation, error) {
	var item CancellationObservation
	err := DB.Where("request_id = ?", requestID).First(&item).Error
	return &item, err
}

func ListCancellationObservations(modelName string, userID, afterID, limit int) ([]CancellationObservation, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := DB.Model(&CancellationObservation{})
	if modelName != "" {
		query = query.Where("model_name = ?", modelName)
	}
	if userID > 0 {
		query = query.Where("user_id = ?", userID)
	}
	if afterID > 0 {
		query = query.Where("id < ?", afterID)
	}
	var rows []CancellationObservation
	err := query.Order("id DESC").Limit(limit).Find(&rows).Error
	return rows, err
}

func ListDueCancellationObservations(now int64, limit int) ([]CancellationObservation, error) {
	var rows []CancellationObservation
	err := DB.Where("next_check_at <= ?", now).Order("next_check_at ASC, id ASC").Limit(limit).Find(&rows).Error
	return rows, err
}

func UpdateCancellationObservation(item CancellationObservation, state, evidence, reason string, now int64) error {
	delay := int64(300)
	if now-item.CreatedAt > 86400 {
		delay = 3600
	}
	// Another worker may have refreshed the row since this scan. Do not let an
	// older observation overwrite newer evidence. No money table is updated.
	return DB.Model(&CancellationObservation{}).
		Where("id = ? AND revision = ?", item.Id, item.Revision).
		Updates(map[string]interface{}{
			"local_state": state, "local_evidence": evidence, "review_reason": reason,
			"last_checked_at": now, "next_check_at": now + delay,
			"revision": item.Revision + 1,
		}).Error
}

func LoadCancellationObservationCursor(now int64) (*CancellationObservationCursor, error) {
	cursor := CancellationObservationCursor{Name: "cancellation_audit_v1", Since: now - 86400}
	if err := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&cursor).Error; err != nil {
		return nil, err
	}
	err := DB.Where("name = ?", cursor.Name).First(&cursor).Error
	return &cursor, err
}

func AdvanceCancellationObservationCursor(old CancellationObservationCursor, afterID int, since int64) error {
	return DB.Model(&CancellationObservationCursor{}).
		Where("name = ? AND after_id = ? AND since = ?", old.Name, old.AfterId, old.Since).
		Updates(map[string]interface{}{"after_id": afterID, "since": since}).Error
}
