package model

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ImagineBatch persists the reservation before any request reaches the provider.
type ImagineBatch struct {
	ID                 string `gorm:"primaryKey;size:64"`
	RequestID          string `gorm:"uniqueIndex;size:64"`
	CallbackToken      string `json:"-" gorm:"uniqueIndex;size:64"`
	UserID             int    `gorm:"index"`
	TokenID            int
	TokenName          string
	ChannelID          int
	Model              string
	Speed              string
	Version            string
	Niji               bool
	Size               string
	Repeat             int
	ActualTaskCount    int
	BaseUnitPrice      float64
	BaseTotalCost      float64
	FinalMultiplier    float64
	UnitQuota          int
	ReservedQuota      int
	RefundedQuota      int
	SettledQuota       int
	BillingSource      string
	SubscriptionID     int
	Group              string
	Status             string `gorm:"index;size:32"`
	Revision           int
	CreatedAt          int64
	UpdatedAt          int64
	RequestData        string `json:"-" gorm:"type:text"`
	SubmissionResponse string `json:"-" gorm:"type:text"`
}

type ImagineTask struct {
	ID                   string `gorm:"primaryKey;size:64"`
	BatchID              string `gorm:"index;size:64"`
	Provider             string `json:"-" gorm:"uniqueIndex:idx_imagine_provider_task;size:32"`
	UpstreamID           string `json:"-" gorm:"uniqueIndex:idx_imagine_provider_task;size:128"`
	Status               string `gorm:"index;size:32"`
	OriginalStatus       string
	Progress             int
	BillingStatus        string
	BillingTransactionID string
	RefundStatus         string
	RefundTransactionID  string
	CreatedAt            int64
	CompletedAt          int64
	SettledAt            int64
	ActualTime           int64
	NextPollAt           int64 `gorm:"index"`
	PollFailures         int
	ExpiresAt            int64
	ProviderCost         float64 `json:"-"`
	CreditsCost          float64 `json:"-"`
	Images               string  `gorm:"type:text"`
	GridURL              string  `gorm:"type:text"`
	ErrorCode            string
	ErrorMessage         string
	ErrorType            string
	ErrorParam           string
	RawResult            string `json:"-" gorm:"type:text"`
	CallbackPayload      string `json:"-" gorm:"type:text"`
	CallbackReceivedAt   int64
	CompletionSource     string
}

// Outbox plus a delivery marker handles deployments with a separate log database.
type ImagineBillingEvent struct {
	ID        string `gorm:"primaryKey;size:128"`
	LogData   string `gorm:"type:text"`
	Delivered bool   `gorm:"index"`
	CreatedAt int64
}
type ImagineLogDelivery struct {
	ID string `gorm:"primaryKey;size:128"`
}

func GetImagineBatch(id string) (*ImagineBatch, error) {
	var batch ImagineBatch
	err := DB.Where("id = ?", id).First(&batch).Error
	return &batch, err
}

func GetImagineTask(id string, userID int) (*ImagineTask, *ImagineBatch, error) {
	var task ImagineTask
	if err := DB.Where("id = ?", id).First(&task).Error; err != nil {
		return nil, nil, err
	}
	batch, err := GetImagineBatch(task.BatchID)
	if err != nil || batch.UserID != userID {
		return nil, nil, gorm.ErrRecordNotFound
	}
	return &task, batch, nil
}

func lockImagineBatch(tx *gorm.DB, id string) (*ImagineBatch, error) {
	res := tx.Model(&ImagineBatch{}).Where("id = ?", id).Update("revision", gorm.Expr("revision + 1"))
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected != 1 {
		return nil, gorm.ErrRecordNotFound
	}
	var batch ImagineBatch
	err := tx.Where("id = ?", id).First(&batch).Error
	return &batch, err
}

// This delta operates under the batch write lock, together with the task and journal.
func imagineRefundTx(tx *gorm.DB, batch *ImagineBatch, quota int) error {
	if quota <= 0 {
		return nil
	}
	if quota > batch.ReservedQuota-batch.RefundedQuota-batch.SettledQuota {
		return errors.New("imagine refund exceeds reservation")
	}
	if batch.BillingSource == "subscription" {
		if err := tx.Model(&UserSubscription{}).Where("id = ?", batch.SubscriptionID).
			Update("amount_used", gorm.Expr("CASE WHEN amount_used >= ? THEN amount_used - ? ELSE 0 END", quota, quota)).Error; err != nil {
			return err
		}
		if err := tx.Model(&SubscriptionPreConsumeRecord{}).Where("request_id = ?", batch.RequestID).
			Update("pre_consumed", gorm.Expr("CASE WHEN pre_consumed >= ? THEN pre_consumed - ? ELSE 0 END", quota, quota)).Error; err != nil {
			return err
		}
	} else {
		if err := tx.Model(&User{}).Where("id = ?", batch.UserID).Update("quota", gorm.Expr("quota + ?", quota)).Error; err != nil {
			return err
		}
	}
	if batch.TokenID > 0 {
		if err := tx.Model(&Token{}).Where("id = ?", batch.TokenID).Updates(map[string]any{
			"remain_quota": gorm.Expr("remain_quota + ?", quota), "used_quota": gorm.Expr("used_quota - ?", quota),
		}).Error; err != nil {
			return err
		}
	}
	batch.RefundedQuota += quota
	return nil
}

func invalidateImagineQuotaCache(batch *ImagineBatch) {
	if !common.RedisEnabled {
		return
	}
	_ = invalidateUserCache(batch.UserID)
	if batch.TokenID > 0 {
		var token Token
		if DB.Select("key").Where("id = ?", batch.TokenID).First(&token).Error == nil {
			_ = cacheDeleteToken(token.Key)
		}
	}
}

func AttachImagineTasks(batchID string, upstreamIDs []string) error {
	var updated *ImagineBatch
	err := DB.Transaction(func(tx *gorm.DB) error {
		batch, err := lockImagineBatch(tx, batchID)
		if err != nil {
			return err
		}
		updated = batch
		if batch.Status != "submitting" && batch.Status != "submission_unknown" {
			return nil
		}
		seen := map[string]bool{}
		for _, id := range upstreamIDs {
			if strings.TrimSpace(id) == "" || len(id) > 128 {
				return errors.New("invalid upstream task id")
			}
			seen[id] = true
		}
		if len(seen) == 0 || len(seen) > batch.Repeat {
			return errors.New("unexpected upstream task count")
		}
		for id := range seen {
			task := ImagineTask{ID: "imagine_" + strings.TrimPrefix(GenerateTaskID(), "task_"), BatchID: batch.ID, Provider: "apimart", UpstreamID: id,
				Status: "queued", OriginalStatus: "submitted", BillingStatus: "reserved", RefundStatus: "none", CreatedAt: time.Now().Unix(), NextPollAt: time.Now().Unix() + 180}
			if err := tx.Create(&task).Error; err != nil {
				return err
			}
		}
		batch.ActualTaskCount = len(seen)
		batch.BaseTotalCost = batch.BaseUnitPrice * float64(len(seen))
		if err := imagineRefundTx(tx, batch, batch.ReservedQuota-batch.UnitQuota*len(seen)); err != nil {
			return err
		}
		batch.Status = "submitted"
		return tx.Save(batch).Error
	})
	if err == nil && updated != nil {
		invalidateImagineQuotaCache(updated)
	}
	return err
}

func RejectImagineSubmission(batchID string) error {
	var updated *ImagineBatch
	err := DB.Transaction(func(tx *gorm.DB) error {
		batch, err := lockImagineBatch(tx, batchID)
		if err != nil {
			return err
		}
		updated = batch
		if batch.Status != "submitting" {
			return nil
		}
		if err := imagineRefundTx(tx, batch, batch.ReservedQuota); err != nil {
			return err
		}
		batch.Status = "rejected"
		return tx.Save(batch).Error
	})
	if err == nil && updated != nil {
		invalidateImagineQuotaCache(updated)
	}
	return err
}

func ApplyImagineResult(taskID string, result ImagineTask) error {
	var original ImagineTask
	if err := DB.Where("id = ?", taskID).First(&original).Error; err != nil {
		return err
	}
	var updated *ImagineBatch
	err := DB.Transaction(func(tx *gorm.DB) error {
		batch, err := lockImagineBatch(tx, original.BatchID)
		if err != nil {
			return err
		}
		updated = batch
		var task ImagineTask
		if err := tx.Where("id = ?", taskID).First(&task).Error; err != nil {
			return err
		}
		if task.Status == "completed" || task.Status == "failed" {
			return nil
		}
		result.ID = task.ID
		result.BatchID = task.BatchID
		result.UpstreamID = task.UpstreamID
		result.Provider = task.Provider
		result.CreatedAt = task.CreatedAt
		result.BillingStatus = task.BillingStatus
		result.RefundStatus = task.RefundStatus
		result.CallbackReceivedAt = task.CallbackReceivedAt
		if result.Status != "completed" && result.Status != "failed" {
			result.NextPollAt = time.Now().Unix() + 180
			if task.Status == "processing" && result.Status == "queued" {
				result.Status = "processing"
			}
			if result.Progress < task.Progress {
				result.Progress = task.Progress
			}
			return tx.Omit("CallbackPayload", "CallbackReceivedAt").Save(&result).Error
		}
		if result.CompletedAt == 0 {
			result.CompletedAt = time.Now().Unix()
		}
		result.SettledAt = time.Now().Unix()
		if result.ActualTime == 0 {
			result.ActualTime = result.CompletedAt - result.CreatedAt
		}
		eventID := task.ID + ":" + result.Status
		logType := LogTypeConsume
		if result.Status == "failed" {
			if err := imagineRefundTx(tx, batch, batch.UnitQuota); err != nil {
				return err
			}
			result.BillingStatus = "refunded"
			result.RefundStatus = "refunded"
			result.RefundTransactionID = eventID
			logType = LogTypeRefund
		} else {
			batch.SettledQuota += batch.UnitQuota
			result.BillingStatus = "settled"
			result.BillingTransactionID = eventID
			if err := tx.Model(&User{}).Where("id = ?", batch.UserID).Updates(map[string]any{"used_quota": gorm.Expr("used_quota + ?", batch.UnitQuota), "request_count": gorm.Expr("request_count + 1")}).Error; err != nil {
				return err
			}
			if err := tx.Model(&Channel{}).Where("id = ?", batch.ChannelID).Update("used_quota", gorm.Expr("used_quota + ?", batch.UnitQuota)).Error; err != nil {
				return err
			}
		}
		if err := tx.Save(&result).Error; err != nil {
			return err
		}
		var pending int64
		if err := tx.Model(&ImagineTask{}).Where("batch_id = ? AND status NOT IN ?", batch.ID, []string{"completed", "failed"}).Count(&pending).Error; err != nil {
			return err
		}
		if pending == 0 {
			batch.Status = "finished"
		}
		if err := tx.Save(batch).Error; err != nil {
			return err
		}
		var images []string
		_ = common.UnmarshalJsonStr(result.Images, &images)
		other := map[string]any{"task_id": task.ID, "request_id": batch.RequestID, "model": batch.Model, "version": batch.Version, "niji": batch.Niji, "speed": batch.Speed, "size": batch.Size, "repeat": batch.Repeat,
			"billing_unit": "generation", "actual_task_count": 1, "request_actual_task_count": batch.ActualTaskCount, "final_multiplier": batch.FinalMultiplier, "billing_status": result.BillingStatus, "refund_status": result.RefundStatus,
			"image_count": len(images), "status": result.Status, "created_at": result.CreatedAt, "completed_at": result.CompletedAt, "actual_time": result.ActualTime, "error_code": result.ErrorCode,
			"admin_info":     map[string]any{"provider": task.Provider, "task_id": task.UpstreamID, "base_unit_price": batch.BaseUnitPrice, "base_total_cost": batch.BaseUnitPrice, "provider_actual_cost": result.ProviderCost, "credits_cost": result.CreditsCost, "error_message": result.ErrorMessage},
			"billing_source": batch.BillingSource, "subscription_id": batch.SubscriptionID}
		if len(images) > 0 {
			other["result_url"] = images[0]
			other["result_urls"] = images
		}
		otherJSON, err := common.Marshal(other)
		if err != nil {
			return err
		}
		row := Log{UserId: batch.UserID, TokenId: batch.TokenID, TokenName: batch.TokenName, ChannelId: batch.ChannelID, Group: batch.Group, ModelName: batch.Model, RequestId: batch.RequestID,
			Type: logType, Quota: batch.UnitQuota, CreatedAt: time.Now().Unix(), UseTime: int(result.ActualTime), Content: "Midjourney Imagine " + result.Status, Other: string(otherJSON)}
		if result.Status == "completed" {
			row.AccountingUserFinalAmountUSD = float64(batch.UnitQuota) / common.QuotaPerUnit
			row.AccountingChannelCostAmountUSD = batch.BaseUnitPrice
			row.AccountingStatus = "settled"
		}
		encoded, err := common.Marshal(row)
		if err != nil {
			return err
		}
		return tx.Create(&ImagineBillingEvent{ID: eventID, LogData: string(encoded), CreatedAt: time.Now().Unix()}).Error
	})
	if err == nil && updated != nil {
		invalidateImagineQuotaCache(updated)
	}
	return err
}

func DeliverImagineBillingEvents() error {
	var events []ImagineBillingEvent
	if err := DB.Where("delivered = ?", false).Order("created_at").Limit(100).Find(&events).Error; err != nil {
		return err
	}
	for _, event := range events {
		err := LOG_DB.Transaction(func(tx *gorm.DB) error {
			res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ImagineLogDelivery{ID: event.ID})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return nil
			}
			var row Log
			if err := common.UnmarshalJsonStr(event.LogData, &row); err != nil {
				return err
			}
			if row.Type == LogTypeConsume {
				var other struct {
					AdminInfo struct {
						BasePrice float64 `json:"base_unit_price"`
					} `json:"admin_info"`
				}
				_ = common.UnmarshalJsonStr(row.Other, &other)
				row.AccountingChannelCostAmountUSD = other.AdminInfo.BasePrice
				row.AccountingUserFinalAmountUSD = float64(row.Quota) / common.QuotaPerUnit
				row.AccountingUserPriceAmountUSD = row.AccountingUserFinalAmountUSD
				row.AccountingStatus = "settled"
			}
			return tx.Create(&row).Error
		})
		if err != nil {
			return fmt.Errorf("deliver imagine billing event: %w", err)
		}
		if err := DB.Model(&ImagineBillingEvent{}).Where("id = ?", event.ID).Update("delivered", true).Error; err != nil {
			return err
		}
	}
	return nil
}
