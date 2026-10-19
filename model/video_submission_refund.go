package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// A refund is independent of upstream acceptance. No generation or settlement
// may be inferred from this receipt. It contains no credential or request body.
type VideoSubmissionRefund struct {
	ID             string `gorm:"primaryKey;size:64"`
	UserID         int    `gorm:"index"`
	TaskID         string `gorm:"size:64;index"`
	RequestID      string `gorm:"size:128;index"`
	WalletQuota    int
	TokenID        int
	TokenQuota     int
	EvidenceSource string `gorm:"size:64"`
	RefundedAt     int64
	LogPublished   bool `gorm:"index"`
	Revision       int
}
type VideoRefundLogDelivery struct {
	ID    string `gorm:"primaryKey;size:64"`
	Owner string `gorm:"size:64"`
}

func FindVideoSubmissionByTaskID(userID int, taskID string) (*VideoSubmission, error) {
	var rows []VideoSubmission
	if err := DB.Where("user_id = ? AND task_id = ?", userID, taskID).Limit(2).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	if len(rows) != 1 {
		return nil, errors.New("ambiguous video submission")
	}
	return &rows[0], nil
}

// The durable refund, wallet and token changes commit together. A duplicate
// caller can only return the original receipt, never credit the wallet again.
func RefundVideoSubmissionPreConsume(row *VideoSubmission, walletQuota, tokenID, tokenQuota int, playground bool) (*VideoSubmissionRefund, error) {
	if row == nil || row.ID == "" || row.UserID <= 0 || walletQuota <= 0 || tokenQuota < 0 {
		return nil, errors.New("invalid video refund")
	}
	var item VideoSubmissionRefund
	err := DB.Transaction(func(tx *gorm.DB) error {
		// Serialize SQLite too, where FOR UPDATE is not supported.
		lock := tx.Model(&VideoSubmission{}).Where("id = ? AND user_id = ?", row.ID, row.UserID).Update("updated_at", gorm.Expr("updated_at"))
		if lock.Error != nil {
			return lock.Error
		}
		if lock.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		var current VideoSubmission
		if err := tx.Where("id = ? AND user_id = ?", row.ID, row.UserID).First(&current).Error; err != nil {
			return err
		}
		err := tx.First(&item, "id = ?", row.ID).Error
		if err == nil {
			if item.UserID != row.UserID || item.TaskID != current.TaskID || item.RequestID != current.RequestID || item.WalletQuota != walletQuota || item.TokenID != tokenID || item.TokenQuota != tokenQuota {
				return errors.New("conflicting video refund evidence")
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if current.TaskReady || current.Status == VideoSubmissionAccepted || current.RecoverySettled || current.RequestID == "" {
			return errors.New("video refund requires an unsettled original")
		}
		var count int64
		if err := tx.Model(&BillingHold{}).Where("request_id = ?", current.RequestID).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return errors.New("video refund already owned by billing hold")
		}
		// A request-body/receipt construction failure can occur after pre-consume
		// but before the send snapshot. Only the still-unsent claim may then use
		// its live BillingSession reservation; post-send unknowns require frozen
		// evidence. Neither branch uses current pricing.
		if current.RecoveryJSON == "" {
			if (current.Status != VideoSubmissionPending && current.Status != VideoSubmissionNotAccepted) || current.UpstreamTaskID != "" {
				return errors.New("video refund missing send snapshot")
			}
		} else {
			var snapshot VideoSubmissionRecovery
			if common.UnmarshalJsonStr(current.RecoveryJSON, &snapshot) != nil || snapshot.Task.TaskID != current.TaskID || snapshot.Task.UserId != current.UserID || snapshot.Task.Quota != walletQuota || snapshot.PrivateData.BillingSource != "wallet" || (!playground && snapshot.PrivateData.TokenId != tokenID) {
				return errors.New("video refund frozen reservation mismatch")
			}
		}
		if !playground && tokenQuota > 0 {
			var token Token
			if err := tx.First(&token, tokenID).Error; err != nil {
				return err
			}
			if token.UserId != current.UserID {
				return errors.New("video refund token owner mismatch")
			}
		}
		if err := adjustWalletAndTokenQuotaTx(tx, current.UserID, walletQuota, tokenID, tokenQuota, playground); err != nil {
			return err
		}
		item = VideoSubmissionRefund{ID: current.ID, UserID: current.UserID, TaskID: current.TaskID, RequestID: current.RequestID, WalletQuota: walletQuota, TokenID: tokenID, TokenQuota: tokenQuota, EvidenceSource: "atomic_preconsume_refund", RefundedAt: common.GetTimestamp()}
		if DB == LOG_DB {
			if err := publishVideoRefundLog(tx, &item, &current); err != nil {
				return err
			}
			item.LogPublished = true
		}
		return tx.Create(&item).Error
	})
	if err != nil {
		return nil, err
	}
	// Cache publication is post-commit and safe on replay.
	_ = InvalidateUserCache(item.UserID)
	if common.RedisEnabled && tokenID > 0 {
		var token Token
		if DB.First(&token, tokenID).Error == nil {
			_ = cacheDeleteToken(token.Key)
		}
	}
	if !item.LogPublished {
		// Funding has committed. A split log DB outage must never trigger a second
		// balance refund; the master retries the durable log outbox.
		if err := PublishVideoSubmissionRefundLog(item.ID); err != nil {
			common.SysLog("video refund log deferred: " + err.Error())
		}
	}
	return &item, nil
}

func publishVideoRefundLog(tx *gorm.DB, item *VideoSubmissionRefund, row *VideoSubmission) error {
	owner := common.GetUUID()
	marker := VideoRefundLogDelivery{ID: item.ID, Owner: owner}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&marker).Error; err != nil {
		return err
	}
	var saved VideoRefundLogDelivery
	if err := tx.First(&saved, "id = ?", item.ID).Error; err != nil {
		return err
	}
	if saved.Owner != owner {
		return nil
	}
	raw, err := common.Marshal(map[string]any{"task_id": item.TaskID, "request_id": item.RequestID, "video_submission_refund_id": item.ID, "evidence_source": item.EvidenceSource, "upstream_acceptance_unverified": row.Status != VideoSubmissionNotAccepted})
	if err != nil {
		return err
	}
	log := Log{UserId: item.UserID, CreatedAt: item.RefundedAt, Type: LogTypeRefund, Quota: item.WalletQuota, TokenId: item.TokenID, ModelName: row.Model, ChannelId: row.ChannelID, RequestId: item.RequestID, Content: "Video submission pre-consumption refunded", Other: string(raw)}
	return tx.Create(&log).Error
}

func PublishVideoSubmissionRefundLog(id string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&VideoSubmissionRefund{}).Where("id = ?", id).Update("revision", gorm.Expr("revision + 1"))
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		var item VideoSubmissionRefund
		if err := tx.First(&item, "id = ?", id).Error; err != nil {
			return err
		}
		if item.LogPublished {
			return nil
		}
		var row VideoSubmission
		if err := tx.Where("id = ? AND user_id = ?", id, item.UserID).First(&row).Error; err != nil {
			return err
		}
		var err error
		if DB == LOG_DB {
			err = publishVideoRefundLog(tx, &item, &row)
		} else {
			err = LOG_DB.Transaction(func(logTx *gorm.DB) error { return publishVideoRefundLog(logTx, &item, &row) })
		}
		if err != nil {
			return err
		}
		return tx.Model(&item).Update("log_published", true).Error
	})
}
func RetryVideoSubmissionRefundLogs() error {
	var rows []VideoSubmissionRefund
	if err := DB.Where("log_published = ?", false).Order("refunded_at ASC, id ASC").Limit(100).Find(&rows).Error; err != nil {
		return err
	}
	var first error
	for _, row := range rows {
		if err := PublishVideoSubmissionRefundLog(row.ID); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Return customer refund evidence even when generation acceptance is unknown.
// Existing refunded holds remain authoritative; this lookup never moves money.
func GetVideoSubmissionRefundDetails(userID int, taskID string) (map[string]any, error) {
	row, err := FindVideoSubmissionByTaskID(userID, taskID)
	if err != nil {
		return nil, err
	}
	quota, refundedAt, source := 0, int64(0), ""
	var item VideoSubmissionRefund
	err = DB.Where("id = ? AND user_id = ?", row.ID, userID).First(&item).Error
	if err == nil {
		quota, refundedAt, source = item.WalletQuota, item.RefundedAt, item.EvidenceSource
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	} else {
		var hold BillingHold
		if err := DB.Where("request_id = ? AND user_id = ? AND status = ?", row.RequestID, userID, BillingHoldStatusRefunded).First(&hold).Error; err != nil {
			return nil, err
		}
		quota, refundedAt, source = hold.PreConsumedQuota, hold.ResolvedAt, "billing_hold_refund"
	}
	if quota <= 0 || refundedAt <= 0 {
		return nil, errors.New("invalid video refund evidence")
	}
	funding := "unknown"
	var snapshot VideoSubmissionRecovery
	if common.UnmarshalJsonStr(row.RecoveryJSON, &snapshot) == nil && snapshot.PrivateData.BillingSource != "" {
		funding = snapshot.PrivateData.BillingSource
	}
	if source != "billing_hold_refund" {
		funding = "wallet"
	}
	amount := decimal.NewFromInt(int64(quota)).Div(decimal.NewFromFloat(common.QuotaPerUnit)).StringFixed(6)
	details := map[string]any{"receipt_id": "refund_" + row.ID, "task_id": row.TaskID, "request_id": row.RequestID, "model": row.Model, "currency": "USD", "settlement_status": "refunded", "funding_source": funding, "initial_quota_units": quota, "refunded_quota_units": quota, "net_quota_units": 0, "precharged_amount": amount, "refunded_amount": amount, "net_amount": "0.000000", "refunded_at": refundedAt, "evidence_source": source, "submission_status": row.Status, "task_ready": row.TaskReady, "new_submission_safe": row.Status == VideoSubmissionNotAccepted, "upstream_charge_status": "unverified"}
	if source == "historical_sync_refund_log" {
		details["refund_recorded_at"] = refundedAt
		details["refunded_at"] = nil
	}
	return details, nil
}
