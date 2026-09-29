package model

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type NowPaymentsAttempt struct {
	Id                 int    `json:"id"`
	TopUpTradeNo       string `json:"topup_trade_no" gorm:"index;type:varchar(255);not null"`
	InvoiceID          string `json:"invoice_id" gorm:"index;type:varchar(64);not null"`
	PaymentID          string `json:"payment_id" gorm:"uniqueIndex;type:varchar(64);not null"`
	PayAddress         string `json:"pay_address" gorm:"type:varchar(255)"`
	PayinExtraID       string `json:"payin_extra_id" gorm:"type:varchar(255)"`
	PayCurrency        string `json:"pay_currency" gorm:"type:varchar(32)"`
	PayAmount          string `json:"pay_amount" gorm:"type:varchar(64)"`
	ActuallyPaid       string `json:"actually_paid" gorm:"type:varchar(64)"`
	Network            string `json:"network" gorm:"type:varchar(64)"`
	PaymentStatus      string `json:"payment_status" gorm:"type:varchar(32);index"`
	Underpaid          bool   `json:"underpaid" gorm:"default:false;index"`
	DuplicatePayment   bool   `json:"duplicate_payment" gorm:"default:false;index"`
	PayinHash          string `json:"payin_hash" gorm:"type:varchar(255);index"`
	DuplicateAlertedAt int64  `json:"duplicate_alerted_at" gorm:"bigint;default:0"`
	AlertClaimUntil    int64  `json:"alert_claim_until" gorm:"bigint;default:0;index"`
	CreatedAt          int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt          int64  `json:"updated_at" gorm:"bigint"`
}

type NowPaymentsReconcileState struct {
	Name            string `gorm:"primaryKey;type:varchar(64)"`
	LastAlertAt     int64  `gorm:"bigint;default:0"`
	AlertLeaseUntil int64  `gorm:"bigint;default:0"`
	ScanLeaseUntil  int64  `gorm:"bigint;default:0"`
}

func ClaimNowPaymentsScan(now int64) (bool, error) {
	state := NowPaymentsReconcileState{Name: "payment-list"}
	if err := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&state).Error; err != nil {
		return false, err
	}
	result := DB.Model(&NowPaymentsReconcileState{}).Where("name = ? AND scan_lease_until <= ?", state.Name, now).Update("scan_lease_until", now+180)
	return result.RowsAffected == 1, result.Error
}

func ReleaseNowPaymentsScan(leaseUntil int64) error {
	return DB.Model(&NowPaymentsReconcileState{}).Where("name = ? AND scan_lease_until = ?", "payment-list", leaseUntil).Update("scan_lease_until", 0).Error
}

func ClaimNowPaymentsReconcileAlert(now int64) (bool, error) {
	state := NowPaymentsReconcileState{Name: "payment-list"}
	if err := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&state).Error; err != nil {
		return false, err
	}
	result := DB.Model(&NowPaymentsReconcileState{}).Where("name = ? AND last_alert_at <= ? AND alert_lease_until <= ?", state.Name, now-3600, now).Update("alert_lease_until", now+120)
	return result.RowsAffected == 1, result.Error
}

func CompleteNowPaymentsReconcileAlert(leaseUntil, deliveredAt int64) error {
	return DB.Model(&NowPaymentsReconcileState{}).Where("name = ? AND alert_lease_until = ?", "payment-list", leaseUntil).Updates(map[string]any{"alert_lease_until": int64(0), "last_alert_at": deliveredAt}).Error
}

func (attempt *NowPaymentsAttempt) BeforeCreate(_ *gorm.DB) error {
	attempt.CreatedAt = common.GetTimestamp()
	attempt.UpdatedAt = attempt.CreatedAt
	return nil
}

func (attempt *NowPaymentsAttempt) BeforeUpdate(_ *gorm.DB) error {
	attempt.UpdatedAt = common.GetTimestamp()
	return nil
}

func (attempt *NowPaymentsAttempt) Normalize() {
	attempt.TopUpTradeNo = strings.TrimSpace(attempt.TopUpTradeNo)
	attempt.InvoiceID = strings.TrimSpace(attempt.InvoiceID)
	attempt.PaymentID = strings.TrimSpace(attempt.PaymentID)
	attempt.PayAddress = strings.TrimSpace(attempt.PayAddress)
	attempt.PayinExtraID = strings.TrimSpace(attempt.PayinExtraID)
	attempt.PayCurrency = strings.ToLower(strings.TrimSpace(attempt.PayCurrency))
	attempt.PayAmount = strings.TrimSpace(attempt.PayAmount)
	attempt.ActuallyPaid = strings.TrimSpace(attempt.ActuallyPaid)
	attempt.Network = strings.ToLower(strings.TrimSpace(attempt.Network))
	attempt.PaymentStatus = strings.ToLower(strings.TrimSpace(attempt.PaymentStatus))
	attempt.PayinHash = strings.TrimSpace(attempt.PayinHash)
}

func GetNowPaymentsAttemptByID(paymentID string) *NowPaymentsAttempt {
	var attempt NowPaymentsAttempt
	if err := DB.Where("payment_id = ?", strings.TrimSpace(paymentID)).First(&attempt).Error; err != nil {
		return nil
	}
	return &attempt
}

func GetNowPaymentsAttemptByIDFromPK(id int) *NowPaymentsAttempt {
	var attempt NowPaymentsAttempt
	if err := DB.First(&attempt, id).Error; err != nil {
		return nil
	}
	return &attempt
}

func GetNowPaymentsAttemptsByOrder(tradeNo string) []NowPaymentsAttempt {
	var attempts []NowPaymentsAttempt
	DB.Where("top_up_trade_no = ?", strings.TrimSpace(tradeNo)).Order("id asc").Find(&attempts)
	return attempts
}

func GetNowPaymentsPaymentByTradeNo(tradeNo string) *NowPaymentsPayment {
	var payment NowPaymentsPayment
	if err := DB.Where("top_up_trade_no = ?", strings.TrimSpace(tradeNo)).First(&payment).Error; err != nil {
		return nil
	}
	return &payment
}

func ListPendingNowPaymentsAlerts(limit int) ([]NowPaymentsAttempt, error) {
	var attempts []NowPaymentsAttempt
	err := DB.Where("duplicate_payment = ? AND duplicate_alerted_at = ? AND alert_claim_until <= ?", true, 0, common.GetTimestamp()).Order("id asc").Limit(limit).Find(&attempts).Error
	return attempts, err
}

func ClaimNowPaymentsAlert(id int, leaseUntil int64) (bool, error) {
	result := DB.Model(&NowPaymentsAttempt{}).Where("id = ? AND duplicate_payment = ? AND duplicate_alerted_at = ? AND alert_claim_until <= ?", id, true, 0, common.GetTimestamp()).Update("alert_claim_until", leaseUntil)
	return result.RowsAffected == 1, result.Error
}

func CompleteNowPaymentsAlert(id int, leaseUntil int64, delivered bool) error {
	updates := map[string]any{"alert_claim_until": int64(0)}
	if delivered {
		updates["duplicate_alerted_at"] = common.GetTimestamp()
	}
	return DB.Model(&NowPaymentsAttempt{}).Where("id = ? AND alert_claim_until = ?", id, leaseUntil).Updates(updates).Error
}
