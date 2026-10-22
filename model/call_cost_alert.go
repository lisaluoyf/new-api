package model

import (
	"context"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm/clause"
)

// CallCostTotal uses the request-time procurement accounting already persisted
// in this project's consume logs, including subsequent task-cost corrections.
type CallCostTotal struct {
	ChannelID       int
	CostUSD         decimal.Decimal
	RequestCount    int64
	UnknownRequests int64
}

func (t CallCostTotal) AmountUSD() decimal.Decimal { return t.CostUSD }

// GetCallCostTotals never reads supplier collections or substitutes user charges.
// Positive partial accounting still contains known procurement costs; missing
// official/user prices must not hide those costs. Refunded consume rows follow
// the existing project's accounting semantics and are excluded.
func GetCallCostTotals(ctx context.Context, start, end int64, channelIDs []int) ([]CallCostTotal, error) {
	if start >= end {
		return nil, fmt.Errorf("invalid call-cost window")
	}
	const known = "accounting_channel_cost_amount_usd > 0 AND accounting_status IN ('ok', 'partial')"
	query := LOG_DB.WithContext(ctx).Model(&Log{}).
		Where("type = ? AND created_at >= ? AND created_at < ?", LogTypeConsume, start, end).
		Where("COALESCE(accounting_status, '') <> ?", AccountingStatusRefunded)
	if len(channelIDs) > 0 {
		query = query.Where("channel_id IN ?", channelIDs)
	}
	var rows []CallCostTotal
	err := query.Select("channel_id, COUNT(*) AS request_count, " +
		"SUM(CASE WHEN " + known + " THEN accounting_channel_cost_amount_usd ELSE 0 END) AS cost_usd, " +
		"SUM(CASE WHEN " + known + " THEN 0 ELSE 1 END) AS unknown_requests").
		Group("channel_id").Order("channel_id").Scan(&rows).Error
	for i := range rows {
		rows[i].CostUSD = rows[i].CostUSD.Round(10)
	}
	return rows, err
}

// CallCostAlertState is shared by instances using the same log database. One
// bounded row per rule/window survives process restarts without depending on Redis.
type CallCostAlertState struct {
	RuleKey    string `gorm:"primaryKey;size:64"`
	LeaseToken string `gorm:"size:64;not null;default:''"`
	LeaseUntil int64  `gorm:"not null;default:0"`
	NextSendAt int64  `gorm:"not null;default:0"`
}

// InitCallCostAlertStore is optional and must never be called by InitLogDB.
// Its caller owns the deadline and handles errors by disabling only this alert.
func InitCallCostAlertStore(ctx context.Context) error {
	if LOG_DB == nil {
		return fmt.Errorf("call-cost log database is unavailable")
	}
	return autoMigrateWithLimits(LOG_DB.WithContext(ctx), &CallCostAlertState{})
}

func AcquireCallCostAlert(ctx context.Context, key string, now time.Time) (string, bool, error) {
	db := LOG_DB.WithContext(ctx)
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&CallCostAlertState{RuleKey: key}).Error; err != nil {
		return "", false, err
	}
	token := common.GetUUID()
	result := db.Model(&CallCostAlertState{}).
		Where("rule_key = ? AND next_send_at <= ? AND lease_until <= ?", key, now.Unix(), now.Unix()).
		Updates(map[string]any{"lease_token": token, "lease_until": now.Add(2 * time.Minute).Unix()})
	return token, result.RowsAffected == 1, result.Error
}

// nextSendAt == 0 releases a failed attempt for retry on the next tick. The
// owner check prevents an expired sender from overwriting a newer lease.
func FinishCallCostAlert(ctx context.Context, key, token string, nextSendAt int64) error {
	result := LOG_DB.WithContext(ctx).Model(&CallCostAlertState{}).
		Where("rule_key = ? AND lease_token = ?", key, token).
		Updates(map[string]any{"lease_token": "", "lease_until": 0, "next_send_at": nextSendAt})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("call-cost alert lease lost")
	}
	return nil
}
