package model

import (
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/tidwall/gjson"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// One durable receipt per task fences concurrent refund callers. Pending rows
// are retried by the master; historical rows reverse statistics only.
type ImageTaskRefund struct {
	ID                string `gorm:"primaryKey;size:64"`
	UserID            int    `gorm:"index"`
	TaskID            string `gorm:"size:191;index"`
	ConsumeLogID      int
	RefundLogID       int
	TokenID           int
	SubscriptionID    int
	Quota             int
	WalletQuota       int
	SubscriptionQuota int
	Mode              string `gorm:"size:32"`
	Status            string `gorm:"size:16;index"`
	Revision          int
	LogData           string `gorm:"type:text"`
	LogPublished      bool   `gorm:"index"`
	CreatedAt         int64
	RefundedAt        int64
}

type ImageRefundLogDelivery struct {
	ID    string `gorm:"primaryKey;size:64"`
	Owner string `gorm:"size:64"`
}

func ImageTaskRefundID(userID int, taskID string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("image-refund-v1:%d:%s", userID, taskID))))
}
func GetImageTaskRefund(userID int, taskID string) (*ImageTaskRefund, error) {
	var item ImageTaskRefund
	err := DB.Where("id = ? AND user_id = ?", ImageTaskRefundID(userID, taskID), userID).First(&item).Error
	return &item, err
}

// Freeze after upstream terminal failure/zero-cost verification. It stores no
// credential, request body or media URL. Never derive amounts from today's price.
func FreezeImageTaskRefund(task *Task, consume *Log, refund *Log, source string, subscriptionID int) (*ImageTaskRefund, error) {
	if task == nil || consume == nil || refund == nil || task.Status != TaskStatusFailure || consume.Type != LogTypeConsume || consume.Quota <= 0 ||
		consume.UserId != task.UserId || refund.UserId != task.UserId || refund.Type != LogTypeRefund || refund.Quota != consume.Quota ||
		gjson.Get(consume.Other, "task_id").String() != task.TaskID {
		return nil, errors.New("invalid image task refund evidence")
	}
	item := &ImageTaskRefund{ID: ImageTaskRefundID(task.UserId, task.TaskID), UserID: task.UserId, TaskID: task.TaskID, ConsumeLogID: consume.Id,
		TokenID: consume.TokenId, SubscriptionID: subscriptionID, Quota: consume.Quota, Mode: "refund", Status: "pending", CreatedAt: common.GetTimestamp()}
	switch source {
	case "wallet":
		item.WalletQuota = item.Quota
	case "subscription":
		if subscriptionID <= 0 || consume.WalletSupplementQuota < 0 || consume.WalletSupplementQuota > item.Quota {
			return nil, errors.New("invalid subscription image refund")
		}
		item.WalletQuota = consume.WalletSupplementQuota
		item.SubscriptionQuota = item.Quota - item.WalletQuota
	default:
		return nil, errors.New("unsupported image refund funding")
	}
	other, _ := common.StrToMap(refund.Other)
	if other == nil {
		other = map[string]interface{}{}
	}
	other["image_refund_event_id"] = item.ID
	other["used_quota_reversed"] = true
	other["consume_log_id"] = consume.Id
	refund.Other = common.MapToJsonStr(other)
	raw, err := common.Marshal(refund)
	if err != nil {
		return nil, err
	}
	item.LogData = string(raw)
	if err := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(item).Error; err != nil {
		return nil, err
	}
	saved, err := GetImageTaskRefund(item.UserID, item.TaskID)
	if err != nil {
		return nil, err
	}
	if saved.Mode != "refund" {
		return saved, nil
	}
	if saved.ConsumeLogID != item.ConsumeLogID || saved.Quota != item.Quota || saved.TokenID != item.TokenID || saved.SubscriptionID != item.SubscriptionID || saved.WalletQuota != item.WalletQuota || saved.SubscriptionQuota != item.SubscriptionQuota {
		return nil, errors.New("conflicting image refund evidence")
	}
	return saved, nil
}

func lockImageRefund(tx *gorm.DB, id string) (*ImageTaskRefund, error) {
	// An actual write also serializes SQLite transactions, which ignore FOR UPDATE.
	result := tx.Model(&ImageTaskRefund{}).Where("id = ?", id).Update("revision", gorm.Expr("revision + 1"))
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, gorm.ErrRecordNotFound
	}
	var item ImageTaskRefund
	err := tx.Where("id = ?", id).First(&item).Error
	return &item, err
}

// Funding, token counters, used_quota and the durable receipt commit together.
// With LOG_DB split, a durable log outbox and delivery marker recover publication.
func ApplyImageTaskRefund(id string) (*ImageTaskRefund, error) {
	var item *ImageTaskRefund
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		item, err = lockImageRefund(tx, id)
		if err != nil {
			return err
		}
		if item.Status == "refunded" {
			return nil
		}
		if item.Status != "pending" || item.Mode != "refund" {
			return errors.New("invalid image refund state")
		}
		var task Task
		if err := tx.Select("id", "status").Where("user_id = ? AND task_id = ?", item.UserID, item.TaskID).First(&task).Error; err != nil {
			return err
		}
		if task.Status != TaskStatusFailure {
			return errors.New("image task is not failed")
		}
		updates := map[string]interface{}{"used_quota": gorm.Expr("CASE WHEN used_quota >= ? THEN used_quota - ? ELSE 0 END", item.Quota, item.Quota)}
		if item.WalletQuota > 0 {
			updates["quota"] = gorm.Expr("quota + ?", item.WalletQuota)
		}
		res := tx.Model(&User{}).Where("id = ?", item.UserID).Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return errors.New("image refund owner missing")
		}
		if item.SubscriptionQuota > 0 {
			res := tx.Model(&UserSubscription{}).Where("id = ? AND user_id = ?", item.SubscriptionID, item.UserID).Update("amount_used", gorm.Expr("CASE WHEN amount_used >= ? THEN amount_used - ? ELSE 0 END", item.SubscriptionQuota, item.SubscriptionQuota))
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected != 1 {
				return errors.New("image refund subscription owner mismatch")
			}
		}
		if item.TokenID > 0 {
			var token Token
			res := tx.Unscoped().Where("id = ?", item.TokenID).Limit(1).Find(&token)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 1 {
				if token.UserId != item.UserID {
					return errors.New("image refund token owner mismatch")
				}
				if err := tx.Unscoped().Model(&Token{}).Where("id = ? AND user_id = ?", item.TokenID, item.UserID).Updates(map[string]interface{}{
					"remain_quota": gorm.Expr("remain_quota + ?", item.Quota), "used_quota": gorm.Expr("used_quota - ?", item.Quota), "accessed_time": common.GetTimestamp(),
				}).Error; err != nil {
					return err
				}
			}
		}
		item.Status = "refunded"
		item.RefundedAt = common.GetTimestamp()
		if DB == LOG_DB {
			if err := publishImageRefundLog(tx, item); err != nil {
				return err
			}
			item.LogPublished = true
		}
		return tx.Save(item).Error
	})
	if err != nil {
		return nil, err
	}
	// Invalidate even on replay, recovering an interrupted post-commit invalidation.
	_ = InvalidateUserCache(item.UserID)
	if item.TokenID > 0 && common.RedisEnabled {
		var token Token
		if DB.Unscoped().First(&token, item.TokenID).Error == nil {
			_ = cacheDeleteToken(token.Key)
		}
	}
	if !item.LogPublished {
		if err := PublishImageTaskRefundLog(item.ID); err != nil {
			return item, err
		}
		item.LogPublished = true
	}
	return item, nil
}

func publishImageRefundLog(tx *gorm.DB, item *ImageTaskRefund) error {
	owner := common.GetUUID()
	marker := &ImageRefundLogDelivery{ID: item.ID, Owner: owner}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(marker).Error; err != nil {
		return err
	}
	var saved ImageRefundLogDelivery
	if err := tx.Where("id = ?", item.ID).First(&saved).Error; err != nil {
		return err
	}
	if saved.Owner != owner {
		return nil
	}
	var row Log
	if err := common.UnmarshalJsonStr(item.LogData, &row); err != nil {
		return err
	}
	if row.Type != LogTypeRefund || row.UserId != item.UserID || row.Quota != item.Quota {
		return errors.New("image refund log evidence mismatch")
	}
	// Serialize the consumption marker with the refund log delivery as well.
	var consume Log
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ? AND type = ?", item.ConsumeLogID, item.UserID, LogTypeConsume).First(&consume).Error; err != nil {
		return err
	}
	if consume.Quota != item.Quota || gjson.Get(consume.Other, "task_id").String() != item.TaskID {
		return errors.New("image consumption log changed")
	}
	other, _ := common.StrToMap(consume.Other)
	if other == nil {
		other = map[string]interface{}{}
	}
	other["task_refunded"] = true
	other["used_quota_reversed"] = true
	other["image_refund_event_id"] = item.ID
	if err := tx.Model(&Log{}).Where("id = ?", consume.Id).Update("other", common.MapToJsonStr(other)).Error; err != nil {
		return err
	}
	return tx.Create(&row).Error
}
func PublishImageTaskRefundLog(id string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		item, err := lockImageRefund(tx, id)
		if err != nil {
			return err
		}
		if item.LogPublished {
			return nil
		}
		if item.Status != "refunded" {
			return errors.New("image refund funding not committed")
		}
		if DB == LOG_DB {
			err = publishImageRefundLog(tx, item)
		} else {
			err = LOG_DB.Transaction(func(logTx *gorm.DB) error { return publishImageRefundLog(logTx, item) })
		}
		if err != nil {
			return err
		}
		return tx.Model(item).Update("log_published", true).Error
	})
}
func RetryImageTaskRefunds() error {
	var rows []ImageTaskRefund
	if err := DB.Where("mode = ? AND (status = ? OR log_published = ?)", "refund", "pending", false).Order("created_at").Limit(100).Find(&rows).Error; err != nil {
		return err
	}
	var first error
	for _, r := range rows {
		if _, err := ApplyImageTaskRefund(r.ID); err != nil && first == nil {
			first = err
		}
	}
	return first
}
