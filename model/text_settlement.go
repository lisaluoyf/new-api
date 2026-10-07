package model

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TextSettlement is a durable billing outbox. It contains no credentials or
// customer request bodies. Quota and accounting are frozen by the live request;
// retries never evaluate today's pricing against yesterday's usage.
type TextSettlement struct {
	Id               int    `gorm:"primaryKey"`
	RequestId        string `gorm:"type:varchar(64);uniqueIndex"`
	UserId           int    `gorm:"index:idx_text_settlement_user_status,priority:1"`
	TokenId          int
	ChannelId        int
	FundingSource    string `gorm:"type:varchar(32)"`
	SubscriptionId   int
	PreConsumedQuota int
	TokenConsumed    int
	Quota            int
	IsPlayground     bool
	Status           string `gorm:"type:varchar(32);index:idx_text_settlement_user_status,priority:2;index:idx_text_settlement_retry,priority:1"`
	CreatedAt        int64
	NextAttemptAt    int64 `gorm:"index:idx_text_settlement_retry,priority:2"`
	SettledAt        int64
	LastError        string `gorm:"type:text"`
	LogPayload       string `gorm:"type:text"`
	LogPublished     bool
}

type settlementLogPayload struct {
	Log        Log
	Accounting AccountingLogFields
}

var ErrSettlementBalance = errors.New("billing settlement balance insufficient")

func FreezeTextSettlement(item *TextSettlement, log *Log, accounting AccountingLogFields) error {
	if item == nil || log == nil || item.RequestId == "" || item.UserId <= 0 || item.Quota < 0 ||
		item.PreConsumedQuota < 0 || item.TokenConsumed < 0 || log.RequestId != item.RequestId || log.UserId != item.UserId || log.Quota != item.Quota || log.Type != LogTypeConsume {
		return errors.New("invalid text settlement")
	}
	if item.FundingSource != "wallet" && item.FundingSource != "subscription" {
		return errors.New("unsupported text settlement source")
	}
	data, err := common.Marshal(settlementLogPayload{Log: *log, Accounting: accounting})
	if err != nil {
		return err
	}
	item.LogPayload = string(data)
	item.Status = "pending"
	item.CreatedAt = common.GetTimestamp()
	item.NextAttemptAt = item.CreatedAt
	if err := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(item).Error; err != nil {
		return err
	}
	var saved TextSettlement
	if err := DB.Where("request_id = ?", item.RequestId).First(&saved).Error; err != nil {
		return err
	}
	if saved.UserId != item.UserId || saved.TokenId != item.TokenId || saved.ChannelId != item.ChannelId ||
		saved.Quota != item.Quota || saved.PreConsumedQuota != item.PreConsumedQuota || saved.TokenConsumed != item.TokenConsumed ||
		saved.FundingSource != item.FundingSource || saved.SubscriptionId != item.SubscriptionId || saved.IsPlayground != item.IsPlayground {
		return errors.New("conflicting text settlement for request id")
	}
	*item = saved
	return nil
}

// ApplyTextSettlement serializes retries with a database row lock and commits
// wallet/subscription, token, statistics and (when colocated) the log together.
func ApplyTextSettlement(id int) (*TextSettlement, error) {
	var item TextSettlement
	didSettle := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&item, id).Error; err != nil {
			return err
		}
		if item.Status == "settled" {
			return nil
		}
		if item.Status != "pending" || (item.FundingSource != "wallet" && item.FundingSource != "subscription") {
			return errors.New("invalid persisted text settlement state")
		}
		delta := item.Quota - item.PreConsumedQuota
		if item.FundingSource == "wallet" {
			q := tx.Model(&User{}).Where("id = ?", item.UserId)
			if delta > 0 {
				q = q.Where("quota >= ?", delta)
			}
			res := q.Update("quota", gorm.Expr("quota - ?", delta))
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected != 1 {
				return ErrSettlementBalance
			}
		} else {
			var sub UserSubscription
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", item.SubscriptionId, item.UserId).First(&sub).Error; err != nil {
				return err
			}
			if sub.AmountTotal > 0 && sub.AmountUsed+int64(delta) > sub.AmountTotal {
				return ErrSettlementBalance
			}
			if sub.AmountUsed+int64(delta) < 0 {
				return errors.New("subscription reservation no longer available")
			}
			if err := tx.Model(&sub).Update("amount_used", gorm.Expr("amount_used + ?", delta)).Error; err != nil {
				return err
			}
			var reservation SubscriptionPreConsumeRecord
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("request_id = ?", item.RequestId).First(&reservation).Error; err != nil {
				return err
			}
			if reservation.UserSubscriptionId != item.SubscriptionId || reservation.PreConsumed != int64(item.PreConsumedQuota) {
				return errors.New("subscription reservation mismatch")
			}
			if err := tx.Model(&reservation).Update("pre_consumed", item.Quota).Error; err != nil {
				return err
			}
		}
		if !item.IsPlayground {
			tokenDelta := item.Quota - item.TokenConsumed
			q := tx.Model(&Token{}).Where("id = ? AND user_id = ?", item.TokenId, item.UserId)
			if tokenDelta > 0 {
				q = q.Where("unlimited_quota = ? OR remain_quota >= ?", true, tokenDelta)
			}
			res := q.Updates(map[string]interface{}{"remain_quota": gorm.Expr("remain_quota - ?", tokenDelta), "used_quota": gorm.Expr("used_quota + ?", tokenDelta), "accessed_time": common.GetTimestamp()})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected != 1 {
				return ErrSettlementBalance
			}
		}
		if err := tx.Model(&User{}).Where("id = ?", item.UserId).Updates(map[string]interface{}{"used_quota": gorm.Expr("used_quota + ?", item.Quota), "request_count": gorm.Expr("request_count + 1")}).Error; err != nil {
			return err
		}
		if err := tx.Model(&Channel{}).Where("id = ?", item.ChannelId).Update("used_quota", gorm.Expr("used_quota + ?", item.Quota)).Error; err != nil {
			return err
		}
		item.Status = "settled"
		item.SettledAt = common.GetTimestamp()
		item.LastError = ""
		if DB == LOG_DB {
			if err := publishSettlementLog(tx, &item); err != nil {
				return err
			}
			item.LogPublished = true
		}
		err := tx.Save(&item).Error
		didSettle = err == nil
		return err
	})
	if err != nil {
		// A rolled-back transaction must not report an in-memory settled state.
		item.Status = "pending"
		item.SettledAt = 0
		_ = DB.First(&item, id).Error
		// Funds are untouched on failure. The persisted usage remains retryable.
		_ = DB.Model(&TextSettlement{}).Where("id = ? AND status = ?", id, "pending").Updates(map[string]interface{}{"last_error": err.Error(), "next_attempt_at": common.GetTimestamp() + 60}).Error
		return &item, err
	}
	if didSettle && common.DataExportEnabled {
		var payload settlementLogPayload
		if common.UnmarshalJsonStr(item.LogPayload, &payload) == nil {
			log := payload.Log
			LogQuotaData(item.UserId, log.Username, log.ModelName, item.Quota, log.CreatedAt, log.PromptTokens+log.CompletionTokens)
		}
	}
	if common.RedisEnabled {
		_ = InvalidateUserCache(item.UserId)
		var token Token
		if DB.Unscoped().First(&token, item.TokenId).Error == nil {
			_ = cacheDeleteToken(token.Key)
		}
	}
	if !item.LogPublished {
		err = PublishTextSettlementLog(item.Id)
		if err != nil {
			_ = DB.Model(&TextSettlement{}).Where("id = ?", item.Id).Updates(map[string]interface{}{"last_error": err.Error(), "next_attempt_at": common.GetTimestamp() + 60}).Error
		}
	}
	return &item, err
}

func publishSettlementLog(db *gorm.DB, item *TextSettlement) error {
	var payload settlementLogPayload
	if err := common.UnmarshalJsonStr(item.LogPayload, &payload); err != nil {
		return err
	}
	log := payload.Log
	a := payload.Accounting
	log.AccountingChannelCostAmountUSD, log.AccountingUserPriceAmountUSD = a.ChannelCostAmountUSD, a.UserPriceAmountUSD
	log.AccountingResellerCostAmountUSD, log.AccountingUserFinalAmountUSD = a.ResellerCostAmountUSD, a.UserFinalAmountUSD
	log.AccountingResellerUserId, log.AccountingResellerRuleId = a.ResellerUserId, a.ResellerRuleId
	log.AccountingResellerDiscountRatio, log.AccountingGroupRatio = a.ResellerDiscountRatio, a.GroupRatio
	log.AccountingStatus, log.AccountingSnapshot = a.Status, a.Snapshot
	other, _ := common.StrToMap(log.Other)
	if other == nil {
		other = map[string]interface{}{}
	}
	other["billing_settlement_id"] = item.Id
	other["billing_settlement_status"] = item.Status
	if item.Status != "settled" {
		log.Type = LogTypeError
		log.Quota = 0
		log.Content = "用量已记录，扣费待结算"
		log.AccountingStatus = "pending_settlement"
		log.AccountingUserFinalAmountUSD = 0
		other["required_quota"] = item.Quota
		admin, _ := other["admin_info"].(map[string]interface{})
		if admin == nil {
			admin = map[string]interface{}{}
		}
		admin["billing_settlement_error"] = item.LastError
		other["admin_info"] = admin
	} else {
		delete(other, "billing_settlement_error")
		if admin, ok := other["admin_info"].(map[string]interface{}); ok {
			delete(admin, "billing_settlement_error")
		}
		delete(other, "required_quota")
	}
	log.Other = common.MapToJsonStr(other)
	// This row is reserved for the outbox, never an unrelated relay error.
	var existing []Log
	if err := db.Where("user_id = ? AND request_id = ?", item.UserId, item.RequestId).Find(&existing).Error; err != nil {
		return err
	}
	for _, old := range existing {
		meta, _ := common.StrToMap(old.Other)
		if fmt.Sprint(meta["billing_settlement_id"]) == fmt.Sprint(item.Id) {
			log.Id = old.Id
			return db.Save(&log).Error
		}
		if old.Type == LogTypeConsume {
			return errors.New("existing consume log for settlement request")
		}
	}
	return db.Create(&log).Error
}

func PublishTextSettlementLog(id int) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var item TextSettlement
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&item, id).Error; err != nil {
			return err
		}
		if item.Status == "settled" && item.LogPublished {
			return nil
		}
		db := LOG_DB
		if DB == LOG_DB {
			db = tx
		}
		if err := publishSettlementLog(db, &item); err != nil {
			return err
		}
		if item.Status == "settled" {
			return tx.Model(&item).Update("log_published", true).Error
		}
		return nil
	})
}

func HasPendingTextSettlement(userID int) (bool, error) {
	var count int64
	err := DB.Model(&TextSettlement{}).Where("user_id = ? AND status = ?", userID, "pending").Count(&count).Error
	return count > 0, err
}

func RetryTextSettlements(now int64) error {
	var items []TextSettlement
	if err := DB.Where("next_attempt_at <= ? AND (status = ? OR (status = ? AND log_published = ?))", now, "pending", "settled", false).Order("next_attempt_at, id").Limit(200).Find(&items).Error; err != nil {
		return err
	}
	for _, item := range items {
		if _, err := ApplyTextSettlement(item.Id); err != nil {
			_ = PublishTextSettlementLog(item.Id)
		}
	}
	return nil
}
