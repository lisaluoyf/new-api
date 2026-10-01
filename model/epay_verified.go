package model

import (
	"errors"
	"math"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Called only after authenticated official query; atomic settlement prevents
// a state write from permanently hiding a failed wallet increment.
func RechargeEpayVerified(trade, method string, paid float64) (*TopUp, int, error) {
	var top TopUp
	var quota int
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("trade_no = ?", trade).First(&top).Error; err != nil {
			return err
		}
		if top.PaymentProvider != PaymentProviderEpay || paid <= 0 || math.IsNaN(paid) || math.IsInf(paid, 0) || math.Abs(top.Money-paid) > 0.000001 {
			return ErrPaymentMethodMismatch
		}
		if top.Status == common.TopUpStatusSuccess {
			return nil
		}
		if top.Status != common.TopUpStatusPending {
			return ErrTopUpStatusInvalid
		}
		quota = int(decimal.NewFromFloat(topUpCreditQuota(&top)).Round(0).IntPart())
		if quota <= 0 {
			return errors.New("invalid credited quota")
		}
		MarkTopUpSuccess(&top)
		top.PaymentMethod = method
		if err := tx.Save(&top).Error; err != nil {
			return err
		}
		result := tx.Model(&User{}).Where("id = ?", top.UserId).Update("quota", gorm.Expr("quota + ?", quota))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("wallet owner missing")
		}
		return nil
	})
	if err == nil && quota > 0 {
		_ = invalidateUserCache(top.UserId)
	}
	return &top, quota, err
}
