package model

import (
	"errors"
	"regexp"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// VerifyPayPalRefundRecovery is read-only. New reversals record exact quota in
// the balance transaction. Legacy reversals require a single exact audit log;
// refunded status or the user's current balance alone is insufficient evidence.
func VerifyPayPalRefundRecovery(top *TopUp) (bool, error) {
	if top == nil || top.PaymentProvider != PaymentProviderPayPal || top.Status != "refunded" || top.Money <= 0 {
		return false, nil
	}
	expectedQuota := int(topUpCreditQuota(top))
	if expectedQuota <= 0 {
		return false, nil
	}
	if top.RefundedAmount != 0 || top.RefundedQuota != 0 {
		return decimal.NewFromFloat(top.RefundedAmount).Equal(decimal.NewFromFloat(top.Money)) && top.RefundedQuota == expectedQuota && top.RefundFrozenAmount == 0 && top.RefundFrozenQuota == 0, nil
	}
	if LOG_DB == nil {
		return false, errors.New("refund audit unavailable")
	}
	var logs []Log
	if err := LOG_DB.Where("user_id = ? AND type = ? AND content LIKE ?", top.UserId, LogTypeRefund, "%"+top.TradeNo+"%").Limit(3).Find(&logs).Error; err != nil {
		return false, err
	}
	if len(logs) != 1 {
		return false, nil
	}
	pattern := regexp.MustCompile(`^PayPal 退款回收额度：退款金额 \$([0-9]+(?:\.[0-9]+)?)（订单 ` + regexp.QuoteMeta(top.TradeNo) + `，refund 已在 PayPal 完成），回收额度 ＄([0-9]+(?:\.[0-9]+)?)$`)
	parts := pattern.FindStringSubmatch(logs[0].Content)
	if len(parts) != 3 {
		return false, nil
	}
	refund, e1 := decimal.NewFromString(parts[1])
	credit, e2 := decimal.NewFromString(parts[2])
	return e1 == nil && e2 == nil && refund.Equal(decimal.NewFromFloat(top.Money)) && credit.Mul(decimal.NewFromFloat(common.QuotaPerUnit)).Equal(decimal.NewFromInt(int64(expectedQuota))), nil
}

// VerifyPayPalSubscriptionRefundRecovery verifies a full purchase refund against
// the exact order cycle, never against the wallet mirror or current balance.
// Renewals/upgrades and superseded cycles need historical restoration evidence
// and remain under review rather than inferring recovery from an inactive plan.
func VerifyPayPalSubscriptionRefundRecovery(trade string, userID int, amount float64) (bool, error) {
	if trade == "" || userID <= 0 || amount <= 0 {
		return false, nil
	}
	var order SubscriptionOrder
	err := DB.Where("trade_no = ?", trade).First(&order).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if order.PaymentProvider != PaymentProviderPayPal || order.UserId != userID ||
		order.Status != common.TopUpStatusRefunded || order.CompleteTime <= 0 ||
		!decimal.NewFromFloat(order.Money).Equal(decimal.NewFromFloat(amount)) ||
		!decimal.NewFromFloat(order.RefundAmount).Equal(decimal.NewFromFloat(amount)) ||
		order.ChargebackAmount != 0 || (order.OrderType != "" && order.OrderType != "purchase") ||
		order.PreviousSubscriptionId != 0 || order.PreviousCycleId != 0 {
		return false, nil
	}
	var cycles []UserSubscription
	if err := DB.Where("current_cycle_id = ?", order.Id).Limit(2).Find(&cycles).Error; err != nil {
		return false, err
	}
	if len(cycles) != 1 {
		return false, nil
	}
	cycle := cycles[0]
	return cycle.UserId == order.UserId && cycle.PlanId == order.PlanId && cycle.Source == "order" &&
		cycle.Status == "cancelled" && cycle.StartTime > 0 && cycle.EndTime >= cycle.StartTime &&
		cycle.EndTime <= GetDBTimestamp(), nil
}

// VerifyPayPalUncreditedTopUp uses the persisted order's atomic settlement
// fields. Successful wallet credits always set complete_time in the same
// transaction as the balance update; a subscription mirror is not wallet proof.
func VerifyPayPalUncreditedTopUp(top *TopUp) (bool, error) {
	if top == nil || top.Id <= 0 || top.TradeNo == "" || top.UserId <= 0 || top.PaymentProvider != PaymentProviderPayPal || top.Money <= 0 {
		return false, nil
	}
	var current TopUp
	if err := DB.Where("id = ? AND trade_no = ? AND user_id = ?", top.Id, top.TradeNo, top.UserId).First(&current).Error; err != nil {
		return false, err
	}
	if current.PaymentProvider != PaymentProviderPayPal || current.Money != top.Money ||
		(current.Status != common.TopUpStatusPending && current.Status != common.TopUpStatusFailed && current.Status != common.TopUpStatusExpired) ||
		current.CompleteTime != 0 || current.CreditedAmount != 0 || current.PayPalCaptureID != "" ||
		current.RefundedAmount != 0 || current.RefundedQuota != 0 || current.RefundFrozenAmount != 0 || current.RefundFrozenQuota != 0 {
		return false, nil
	}
	var subscriptions int64
	if err := DB.Model(&SubscriptionOrder{}).Where("trade_no = ?", current.TradeNo).Count(&subscriptions).Error; err != nil {
		return false, err
	}
	return subscriptions == 0, nil
}
