package model

import (
	"errors"
	"regexp"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
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
