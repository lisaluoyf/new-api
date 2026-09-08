package model

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	TrialLimitNotificationThresholdPercent = 90
	TrialLimitNotificationPending          = "pending"
	TrialLimitNotificationSent             = "sent"
	TrialLimitNotificationClicked          = "clicked"
	TrialLimitNotificationConverted        = "converted"
	TrialLimitNotificationFailed           = "failed"
	TrialLimitNotificationUnreachable      = "unreachable"
	TrialLimitNotificationTelegram         = "telegram"
	TrialLimitNotificationDiscord          = "discord"
	TrialLimitNotificationStandard         = "standard"
	TrialLimitNotificationFirstTopupPromo  = "first_topup_promo"
)

// TrialLimitNotification is the durable lifecycle record for one GPT trial
// card reaching its near-exhaustion threshold. The user/subscription index is
// deliberately unique so retries and concurrent settled requests cannot send
// a duplicate message.
type TrialLimitNotification struct {
	Id                int    `json:"id"`
	UserId            int    `json:"user_id" gorm:"not null;uniqueIndex:idx_trial_limit_notification_once,priority:1;index"`
	SubscriptionId    int    `json:"subscription_id" gorm:"not null;uniqueIndex:idx_trial_limit_notification_once,priority:2;index"`
	Threshold         int    `json:"threshold" gorm:"not null;default:90"`
	Channel           string `json:"channel" gorm:"type:varchar(16);default:'';index"`
	Variant           string `json:"variant" gorm:"type:varchar(32);default:'';index"`
	ClickToken        string `json:"click_token" gorm:"type:varchar(64);uniqueIndex;not null"`
	Status            string `json:"status" gorm:"type:varchar(16);not null;default:'pending';index"`
	ProviderMsgID     string `json:"provider_message_id" gorm:"type:varchar(64);default:''"`
	LastError         string `json:"last_error" gorm:"type:text"`
	SentAt            int64  `json:"sent_at" gorm:"type:bigint;default:0;index"`
	ClickedAt         int64  `json:"clicked_at" gorm:"type:bigint;default:0;index"`
	ConversionAt      int64  `json:"conversion_at" gorm:"type:bigint;default:0;index"`
	ConversionTradeNo string `json:"conversion_trade_no" gorm:"type:varchar(255);default:''"`
	CreatedAt         int64  `json:"created_at" gorm:"type:bigint;index"`
	UpdatedAt         int64  `json:"updated_at" gorm:"type:bigint"`
}

func (n *TrialLimitNotification) BeforeCreate(tx *gorm.DB) error {
	now := common.GetTimestamp()
	n.CreatedAt = now
	n.UpdatedAt = now
	return nil
}

func (n *TrialLimitNotification) BeforeUpdate(tx *gorm.DB) error {
	n.UpdatedAt = common.GetTimestamp()
	return nil
}

type TrialLimitNotificationDailyMetrics struct {
	StartAt            int64   `json:"start_at"`
	EndAt              int64   `json:"end_at"`
	TriggeredUsers     int64   `json:"triggered_users"`
	Sent               int64   `json:"sent"`
	TelegramSent       int64   `json:"telegram_sent"`
	DiscordSent        int64   `json:"discord_sent"`
	Failed             int64   `json:"failed"`
	Unreachable        int64   `json:"unreachable"`
	Clicked            int64   `json:"clicked"`
	Converted          int64   `json:"converted"`
	ConvertedWithin24h int64   `json:"converted_within_24h"`
	ConvertedWithin7d  int64   `json:"converted_within_7d"`
	ClickRate          float64 `json:"click_rate"`
	ConversionRate     float64 `json:"conversion_rate"`
}

func newTrialLimitClickToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// CreateTrialLimitNotificationIfReached creates the outbox record only after
// the settled subscription state has crossed the threshold. It returns nil
// when the subscription is not a GPT trial, is below threshold, or was already
// notified.
func CreateTrialLimitNotificationIfReached(userID, subscriptionID int) (*TrialLimitNotification, error) {
	if userID <= 0 || subscriptionID <= 0 {
		return nil, errors.New("invalid trial limit notification identity")
	}
	var created *TrialLimitNotification
	err := DB.Transaction(func(tx *gorm.DB) error {
		var subscription UserSubscription
		if err := tx.Where("id = ? AND user_id = ?", subscriptionID, userID).First(&subscription).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if subscription.AmountTotal <= 0 || subscription.AmountUsed*100 < subscription.AmountTotal*TrialLimitNotificationThresholdPercent {
			return nil
		}
		plan, err := getSubscriptionPlanByIdTx(tx, subscription.PlanId)
		if err != nil {
			return err
		}
		if !IsGPTTrialSubscriptionPlan(plan) {
			return nil
		}
		token, err := newTrialLimitClickToken()
		if err != nil {
			return fmt.Errorf("generate trial notification click token: %w", err)
		}
		notification := &TrialLimitNotification{
			UserId:         userID,
			SubscriptionId: subscriptionID,
			Threshold:      TrialLimitNotificationThresholdPercent,
			ClickToken:     token,
			Status:         TrialLimitNotificationPending,
		}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(notification)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected > 0 {
			created = notification
		}
		return nil
	})
	return created, err
}

func GetTrialLimitNotificationByToken(token string) (*TrialLimitNotification, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("empty trial limit click token")
	}
	var notification TrialLimitNotification
	if err := DB.Where("click_token = ?", token).First(&notification).Error; err != nil {
		return nil, err
	}
	return &notification, nil
}

func MarkTrialLimitNotificationSent(id int, channel, variant, providerMessageID string) error {
	return DB.Model(&TrialLimitNotification{}).Where("id = ? AND status = ?", id, TrialLimitNotificationPending).Updates(map[string]any{
		"status":          TrialLimitNotificationSent,
		"channel":         channel,
		"variant":         variant,
		"provider_msg_id": providerMessageID,
		"last_error":      "",
		"sent_at":         common.GetTimestamp(),
	}).Error
}

func MarkTrialLimitNotificationUnavailable(id int, cause error) error {
	message := "no reachable Telegram or Discord identity"
	if cause != nil {
		message = cause.Error()
	}
	return updateTrialLimitNotificationFailure(id, TrialLimitNotificationUnreachable, message)
}

func MarkTrialLimitNotificationFailed(id int, cause error) error {
	message := "notification delivery failed"
	if cause != nil {
		message = cause.Error()
	}
	return updateTrialLimitNotificationFailure(id, TrialLimitNotificationFailed, message)
}

func updateTrialLimitNotificationFailure(id int, status, message string) error {
	return DB.Model(&TrialLimitNotification{}).Where("id = ? AND status = ?", id, TrialLimitNotificationPending).Updates(map[string]any{
		"status":     status,
		"last_error": message,
	}).Error
}

// MarkTrialLimitNotificationClicked records the first click only. A converted
// record is intentionally left unchanged if a delayed redirect is retried.
func MarkTrialLimitNotificationClicked(token string) (*TrialLimitNotification, bool, error) {
	notification, err := GetTrialLimitNotificationByToken(token)
	if err != nil {
		return nil, false, err
	}
	if notification.Status != TrialLimitNotificationSent {
		return notification, false, nil
	}
	now := common.GetTimestamp()
	result := DB.Model(&TrialLimitNotification{}).Where("id = ? AND status = ? AND clicked_at = 0", notification.Id, TrialLimitNotificationSent).Updates(map[string]any{
		"status":     TrialLimitNotificationClicked,
		"clicked_at": now,
	})
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected > 0 {
		notification.Status = TrialLimitNotificationClicked
		notification.ClickedAt = now
		return notification, true, nil
	}
	current, err := GetTrialLimitNotificationByToken(token)
	return current, false, err
}

// MarkTrialLimitNotificationConverted attributes a successful wallet payment
// to the notification recipient, regardless of whether the redirect click was
// available (for example, a user may reopen the wallet later).
func MarkTrialLimitNotificationConverted(userID int, tradeNo string) error {
	if userID <= 0 || strings.TrimSpace(tradeNo) == "" {
		return nil
	}
	return DB.Model(&TrialLimitNotification{}).
		Where("user_id = ? AND status IN ? AND conversion_at = 0", userID, []string{TrialLimitNotificationSent, TrialLimitNotificationClicked}).
		Updates(map[string]any{
			"status":              TrialLimitNotificationConverted,
			"conversion_at":       common.GetTimestamp(),
			"conversion_trade_no": tradeNo,
		}).Error
}

func GetTrialLimitNotificationDailyMetrics(startAt, endAt int64) (TrialLimitNotificationDailyMetrics, error) {
	metrics := TrialLimitNotificationDailyMetrics{StartAt: startAt, EndAt: endAt}
	if endAt <= startAt {
		return metrics, errors.New("invalid trial notification metrics range")
	}
	query := func() *gorm.DB {
		return DB.Model(&TrialLimitNotification{}).Where("created_at >= ? AND created_at < ?", startAt, endAt)
	}
	if err := query().Count(&metrics.TriggeredUsers).Error; err != nil {
		return metrics, err
	}
	if err := query().Where("sent_at > 0").Count(&metrics.Sent).Error; err != nil {
		return metrics, err
	}
	if err := query().Where("sent_at > 0 AND channel = ?", TrialLimitNotificationTelegram).Count(&metrics.TelegramSent).Error; err != nil {
		return metrics, err
	}
	if err := query().Where("sent_at > 0 AND channel = ?", TrialLimitNotificationDiscord).Count(&metrics.DiscordSent).Error; err != nil {
		return metrics, err
	}
	if err := query().Where("status = ?", TrialLimitNotificationFailed).Count(&metrics.Failed).Error; err != nil {
		return metrics, err
	}
	if err := query().Where("status = ?", TrialLimitNotificationUnreachable).Count(&metrics.Unreachable).Error; err != nil {
		return metrics, err
	}
	if err := query().Where("clicked_at > 0").Count(&metrics.Clicked).Error; err != nil {
		return metrics, err
	}
	if err := query().Where("conversion_at > 0").Count(&metrics.Converted).Error; err != nil {
		return metrics, err
	}
	if err := query().Where("conversion_at > 0 AND sent_at > 0 AND conversion_at <= sent_at + ?", int64(24*3600)).Count(&metrics.ConvertedWithin24h).Error; err != nil {
		return metrics, err
	}
	if err := query().Where("conversion_at > 0 AND sent_at > 0 AND conversion_at <= sent_at + ?", int64(7*24*3600)).Count(&metrics.ConvertedWithin7d).Error; err != nil {
		return metrics, err
	}
	if metrics.Sent > 0 {
		metrics.ClickRate = float64(metrics.Clicked) / float64(metrics.Sent)
		metrics.ConversionRate = float64(metrics.Converted) / float64(metrics.Sent)
	}
	return metrics, nil
}
