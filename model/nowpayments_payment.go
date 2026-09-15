package model

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

type NowPaymentsPayment struct {
	Id              int    `json:"id"`
	TopUpTradeNo    string `json:"topup_trade_no" gorm:"uniqueIndex;type:varchar(255);not null"`
	InvoiceID       string `json:"invoice_id" gorm:"index;type:varchar(64)"`
	InvoiceURL      string `json:"invoice_url" gorm:"type:text"`
	PaymentID       string `json:"payment_id" gorm:"uniqueIndex;type:varchar(64);not null"`
	PayAddress      string `json:"pay_address" gorm:"type:varchar(255);not null"`
	PayinExtraID    string `json:"payin_extra_id" gorm:"type:varchar(255);default:''"`
	PayCurrency     string `json:"pay_currency" gorm:"type:varchar(32);not null"`
	PayAmount       string `json:"pay_amount" gorm:"type:varchar(64);not null"`
	ActuallyPaid    string `json:"actually_paid" gorm:"type:varchar(64);default:''"`
	Network         string `json:"network" gorm:"type:varchar(64);default:''"`
	PaymentStatus   string `json:"payment_status" gorm:"type:varchar(32);not null;default:'waiting';index"`
	PayinHash       string `json:"payin_hash" gorm:"type:varchar(255);default:''"`
	ProviderPayload string `json:"-" gorm:"type:text"`
	ExpiresAt       int64  `json:"expires_at" gorm:"bigint;default:0;index"`
	CreatedAt       int64  `json:"created_at" gorm:"bigint;index"`
	UpdatedAt       int64  `json:"updated_at" gorm:"bigint;index"`
}

func (payment *NowPaymentsPayment) BeforeCreate(_ *gorm.DB) error {
	now := common.GetTimestamp()
	payment.CreatedAt = now
	payment.UpdatedAt = now
	return payment.normalize()
}

func (payment *NowPaymentsPayment) BeforeUpdate(_ *gorm.DB) error {
	payment.UpdatedAt = common.GetTimestamp()
	return payment.normalize()
}

func (payment *NowPaymentsPayment) normalize() error {
	payment.TopUpTradeNo = strings.TrimSpace(payment.TopUpTradeNo)
	payment.InvoiceID = strings.TrimSpace(payment.InvoiceID)
	payment.InvoiceURL = strings.TrimSpace(payment.InvoiceURL)
	payment.PaymentID = strings.TrimSpace(payment.PaymentID)
	payment.PayAddress = strings.TrimSpace(payment.PayAddress)
	payment.PayinExtraID = strings.TrimSpace(payment.PayinExtraID)
	payment.PayCurrency = strings.ToLower(strings.TrimSpace(payment.PayCurrency))
	payment.PayAmount = strings.TrimSpace(payment.PayAmount)
	payment.ActuallyPaid = strings.TrimSpace(payment.ActuallyPaid)
	payment.Network = strings.ToLower(strings.TrimSpace(payment.Network))
	payment.PaymentStatus = strings.ToLower(strings.TrimSpace(payment.PaymentStatus))
	payment.PayinHash = strings.TrimSpace(payment.PayinHash)
	if payment.PaymentStatus == "" {
		payment.PaymentStatus = "waiting"
	}
	return nil
}

func GetNowPaymentsPaymentByID(paymentID string) *NowPaymentsPayment {
	var payment NowPaymentsPayment
	if err := DB.Where("payment_id = ?", strings.TrimSpace(paymentID)).First(&payment).Error; err != nil {
		return nil
	}
	return &payment
}

func GetNowPaymentsPaymentByInvoiceID(invoiceID string) *NowPaymentsPayment {
	var payment NowPaymentsPayment
	if err := DB.Where("invoice_id = ?", strings.TrimSpace(invoiceID)).First(&payment).Error; err != nil {
		return nil
	}
	return &payment
}

func GetNowPaymentsPaymentByOrder(invoiceID, tradeNo string) *NowPaymentsPayment {
	var payment NowPaymentsPayment
	if err := DB.Where("invoice_id = ? AND top_up_trade_no = ?", strings.TrimSpace(invoiceID), strings.TrimSpace(tradeNo)).First(&payment).Error; err != nil {
		return nil
	}
	return &payment
}

func (payment *NowPaymentsPayment) Insert() error { return DB.Create(payment).Error }

func (payment *NowPaymentsPayment) Update() error { return DB.Save(payment).Error }
