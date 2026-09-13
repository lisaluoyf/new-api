package model

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Wallet quota is spendable balance. Pending refunds move quota out of it;
// success converts that reservation to a refund without charging it again.
type WaffoRefund struct {
	ID                int     `json:"id"`
	TicketID          string  `json:"ticket_id" gorm:"type:varchar(64);uniqueIndex"`
	PaymentID         string  `json:"payment_id" gorm:"type:varchar(64);index"`
	OrderID           string  `json:"order_id" gorm:"type:varchar(64)"`
	StoreID           string  `json:"store_id" gorm:"type:varchar(64)"`
	TradeNo           string  `json:"trade_no" gorm:"type:varchar(255);index"`
	UserID            int     `json:"user_id" gorm:"index"`
	Status            string  `json:"status" gorm:"type:varchar(32);index"`
	Amount            float64 `json:"amount" gorm:"type:decimal(18,6)"`
	PaymentAmount     float64 `json:"payment_amount" gorm:"type:decimal(18,6)"`
	Currency          string  `json:"currency" gorm:"type:varchar(8)"`
	Version           int     `json:"version"`
	ProviderUpdatedAt int64   `json:"provider_updated_at"`
	Reason            string  `json:"reason" gorm:"type:text"`
	UpdatedAt         int64   `json:"updated_at" gorm:"autoUpdateTime:false"`
}

func waffoRefundPending(status string) bool {
	return status == "pending" || status == "under_review" || status == "approved" || status == "processing"
}

func ApplyWaffoRefund(in WaffoRefund) error {
	if in.TicketID == "" || in.PaymentID == "" || in.OrderID == "" || in.StoreID == "" || in.TradeNo == "" || in.ProviderUpdatedAt <= 0 || in.Version < 1 {
		return errors.New("incomplete Waffo refund identity")
	}
	if in.Currency != "USD" || math.IsNaN(in.Amount) || math.IsInf(in.Amount, 0) || math.IsNaN(in.PaymentAmount) || math.IsInf(in.PaymentAmount, 0) || in.Amount <= 0 || in.PaymentAmount <= 0 || in.Amount > in.PaymentAmount {
		return errors.New("invalid Waffo refund amount or currency")
	}
	switch in.Status {
	case "pending", "under_review", "approved", "processing", "succeeded", "failed", "rejected", "returned", "cancelled", "canceled":
	default:
		return fmt.Errorf("unknown Waffo refund status %q", in.Status)
	}
	var userID int
	err := DB.Transaction(func(tx *gorm.DB) error {
		var topUp TopUp
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("trade_no = ?", in.TradeNo).First(&topUp).Error; err != nil {
			return err
		}
		if topUp.PaymentProvider != PaymentProviderWaffoPancake || topUp.WaffoPaymentID != in.PaymentID || topUp.WaffoOrderID != in.OrderID {
			return errors.New("Waffo refund payment identity mismatch")
		}
		var subscriptions int64
		if err := tx.Model(&SubscriptionOrder{}).Where("trade_no = ?", in.TradeNo).Count(&subscriptions).Error; err != nil {
			return err
		}
		if subscriptions != 0 {
			return errors.New("Waffo subscription refund requires entitlement review; wallet unchanged")
		}
		if topUp.Status != common.TopUpStatusSuccess && topUp.Status != common.TopUpStatusRefunded {
			return ErrTopUpStatusInvalid
		}
		userID = topUp.UserId
		if topUp.Status == common.TopUpStatusRefunded && topUp.RefundedQuota == 0 {
			return errors.New("Waffo order was refunded outside the refund ledger; manual reconciliation required")
		}
		var previous WaffoRefund
		err := tx.Where("ticket_id = ?", in.TicketID).First(&previous).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			if previous.TradeNo != in.TradeNo || previous.PaymentID != in.PaymentID || previous.StoreID != in.StoreID || previous.PaymentAmount != in.PaymentAmount {
				return errors.New("Waffo refund identity changed")
			}
			if previous.Status == "succeeded" || in.Version < previous.Version || in.ProviderUpdatedAt < previous.ProviderUpdatedAt {
				return nil
			}
			if in.ProviderUpdatedAt == previous.ProviderUpdatedAt && in.Version == previous.Version && in.Status == previous.Status && in.Amount == previous.Amount {
				return nil
			}
			// A failed/rejected attempt may only be reopened by a new submission.
			if !waffoRefundPending(previous.Status) && waffoRefundPending(in.Status) && in.Version <= previous.Version {
				return nil
			}
			in.ID = previous.ID
		}
		in.UserID = userID
		in.UpdatedAt = common.GetTimestamp()
		if err := tx.Save(&in).Error; err != nil {
			return err
		}
		var entries []WaffoRefund
		if err := tx.Where("trade_no = ?", in.TradeNo).Find(&entries).Error; err != nil {
			return err
		}
		frozen, refunded := decimal.Zero, decimal.Zero
		for _, entry := range entries {
			if entry.PaymentAmount != in.PaymentAmount {
				return errors.New("inconsistent Waffo payment amount")
			}
			if waffoRefundPending(entry.Status) {
				frozen = frozen.Add(decimal.NewFromFloat(entry.Amount))
			} else if entry.Status == "succeeded" {
				refunded = refunded.Add(decimal.NewFromFloat(entry.Amount))
			}
		}
		paid := decimal.NewFromFloat(in.PaymentAmount)
		if frozen.Add(refunded).GreaterThan(paid) {
			return errors.New("cumulative Waffo refunds exceed original payment")
		}
		credit := decimal.NewFromFloat(topUpCreditQuota(&topUp)).Round(0)
		// Round the cumulative reservation once so multiple partial refunds
		// can never reserve more than the original credit.
		reservedQuota := int(credit.Mul(frozen.Add(refunded)).Div(paid).Round(0).IntPart())
		refundedQuota := int(credit.Mul(refunded).Div(paid).Round(0).IntPart())
		delta := topUp.RefundFrozenQuota + topUp.RefundedQuota - reservedQuota
		if delta != 0 {
			res := tx.Model(&User{}).Where("id = ?", userID).Update("quota", gorm.Expr("quota + ?", delta))
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected != 1 {
				return errors.New("Waffo refund user not found")
			}
		}
		status := common.TopUpStatusSuccess
		if refunded.Equal(paid) {
			status = common.TopUpStatusRefunded
		}
		return tx.Model(&topUp).Updates(map[string]any{
			"refund_frozen_amount": frozen.InexactFloat64(), "refunded_amount": refunded.InexactFloat64(),
			"refund_frozen_quota": reservedQuota - refundedQuota, "refunded_quota": refundedQuota, "status": status,
		}).Error
	})
	if err != nil {
		return err
	}
	// Also invalidate on idempotent retries, recovering from a cache outage
	// after the database commit. Never retry the financial adjustment itself.
	return invalidateUserCache(userID)
}

func BindWaffoPayment(tradeNo, paymentID, orderID string) error {
	if strings.TrimSpace(tradeNo) == "" || paymentID == "" || orderID == "" {
		return errors.New("missing Waffo payment reference")
	}
	result := DB.Model(&TopUp{}).Where("trade_no = ? AND payment_provider = ?", tradeNo, PaymentProviderWaffoPancake).
		Where("(waffo_payment_id IS NULL OR waffo_payment_id = '' OR waffo_payment_id = ?) AND (waffo_order_id IS NULL OR waffo_order_id = '' OR waffo_order_id = ?)", paymentID, orderID).
		Updates(map[string]any{"waffo_payment_id": paymentID, "waffo_order_id": orderID})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		// MySQL may report zero affected rows for an unchanged binding.
		var count int64
		if err := DB.Model(&TopUp{}).Where("trade_no = ? AND payment_provider = ? AND waffo_payment_id = ? AND waffo_order_id = ?", tradeNo, PaymentProviderWaffoPancake, paymentID, orderID).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return errors.New("Waffo payment reference conflicts or order is missing")
		}
	}
	return nil
}
