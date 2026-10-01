package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

const (
	PlategaPaymentMethodSBPQR = "SBPQR"

	PlategaStatusPending    = "pending"
	PlategaStatusConfirmed  = "confirmed"
	PlategaStatusCanceled   = "canceled"
	PlategaStatusChargeback = "chargeback"

	PlategaAPIStatusConfirmed  = "CONFIRMED"
	PlategaAPIStatusCanceled   = "CANCELED"
	PlategaAPIStatusChargeback = "CHARGEBACKED"
)

// PlategaOrder stores Platega-specific payment metadata linked to top_ups.trade_no.
type PlategaOrder struct {
	Id                    int     `json:"id"`
	TradeNo               string  `json:"trade_no" gorm:"uniqueIndex;type:varchar(255)"`
	UserId                int     `json:"user_id" gorm:"index"`
	RubAmount             float64 `json:"rub_amount"`
	UsdQuotaAmount        int64   `json:"usd_quota_amount"`
	PlategaTransactionId  string  `json:"platega_transaction_id" gorm:"uniqueIndex;type:varchar(255)"`
	PaymentMethod         string  `json:"payment_method" gorm:"type:varchar(32);default:SBPQR"`
	PlategaStatus         string  `json:"platega_status" gorm:"type:varchar(32);index"`
	Payload               string  `json:"payload" gorm:"type:varchar(255);index"`
	CreateRequestJSON     string  `json:"create_request_json" gorm:"type:text"`
	CreateResponseJSON    string  `json:"create_response_json" gorm:"type:text"`
	CallbackJSON          string  `json:"callback_json" gorm:"type:text"`
	CallbackHeadersJSON   string  `json:"callback_headers_json" gorm:"type:text"`
	CreateTime            int64   `json:"create_time"`
	UpdateTime            int64   `json:"update_time"`
	GrantedQuota          int64   `json:"granted_quota" gorm:"not null;default:0"`
	ReversedQuota         int64   `json:"reversed_quota" gorm:"not null;default:0"`
	ReversedRub           float64 `json:"reversed_rub" gorm:"type:decimal(18,2);not null;default:0"`
	GrantedSubscriptionID int     `json:"granted_subscription_id" gorm:"not null;default:0"`
	ReviewReason          string  `json:"review_reason" gorm:"type:text"`
}

func (o *PlategaOrder) Insert() error {
	return DB.Create(o).Error
}

func (o *PlategaOrder) Save() error {
	o.UpdateTime = common.GetTimestamp()
	return DB.Save(o).Error
}

func GetPlategaOrderByTradeNo(tradeNo string) *PlategaOrder {
	var order PlategaOrder
	if err := DB.Where("trade_no = ?", tradeNo).First(&order).Error; err != nil {
		return nil
	}
	return &order
}

func GetPlategaOrderByTransactionId(transactionId string) *PlategaOrder {
	if strings.TrimSpace(transactionId) == "" {
		return nil
	}
	var order PlategaOrder
	if err := DB.Where("platega_transaction_id = ?", transactionId).First(&order).Error; err != nil {
		return nil
	}
	return &order
}

func GetPlategaOrderByPayload(payload string) *PlategaOrder {
	if strings.TrimSpace(payload) == "" {
		return nil
	}
	var order PlategaOrder
	if err := DB.Where("payload = ?", payload).First(&order).Error; err != nil {
		return nil
	}
	return &order
}

func ListPlategaOrders(limit, offset int) ([]*PlategaOrder, int64, error) {
	var orders []*PlategaOrder
	var total int64
	tx := DB.Model(&PlategaOrder{})
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := tx.Order("id desc").Limit(limit).Offset(offset).Find(&orders).Error; err != nil {
		return nil, 0, err
	}
	return orders, total, nil
}

func NormalizePlategaAPIStatus(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case PlategaAPIStatusConfirmed:
		return PlategaStatusConfirmed
	case PlategaAPIStatusCanceled:
		return PlategaStatusCanceled
	case PlategaAPIStatusChargeback, "CHARGEBACK": // Historical integration spelling.
		return PlategaStatusChargeback
	case "PENDING", "PENDING_PAYMENT", "IN_PROGRESS":
		return PlategaStatusPending
	default:
		return strings.ToLower(strings.TrimSpace(status))
	}
}

func (o *PlategaOrder) ApplyPlategaStatus(status string) error {
	if o == nil {
		return errors.New("missing platega order")
	}
	normalized := NormalizePlategaAPIStatus(status)
	if normalized == "" {
		return fmt.Errorf("unknown platega status: %s", status)
	}
	o.PlategaStatus = normalized
	o.UpdateTime = common.GetTimestamp()
	return o.Save()
}
