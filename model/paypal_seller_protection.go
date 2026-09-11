package model

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// PayPalSellerProtection is a snapshot of PayPal's eligibility assessment,
// not a guarantee that a future dispute will be covered.
type PayPalSellerProtection struct {
	Status            string   `json:"status" gorm:"type:varchar(32);default:''"`
	DisputeCategories []string `json:"dispute_categories,omitempty" gorm:"serializer:json;type:text"`
}

type PayPalCaptureMetadata struct {
	ID               string                 `json:"id"`
	SellerProtection PayPalSellerProtection `json:"seller_protection"`
}

func (topUp *TopUp) ApplyPayPalCapture(capture PayPalCaptureMetadata) {
	if topUp.PaymentProvider != PaymentProviderPayPal {
		return
	}
	if capture.ID != "" {
		topUp.PayPalCaptureID = capture.ID
	}
	switch capture.SellerProtection.Status {
	case "ELIGIBLE", "PARTIALLY_ELIGIBLE", "NOT_ELIGIBLE":
		topUp.PayPalSellerProtection = capture.SellerProtection
	}
}

func applySubscriptionPayPalCapture(topUp *TopUp, order *SubscriptionOrder) {
	if order.PaymentProvider != PaymentProviderPayPal || order.ProviderPayload == "" {
		return
	}
	var capture PayPalCaptureMetadata
	if common.UnmarshalJsonStr(order.ProviderPayload, &capture) == nil {
		topUp.ApplyPayPalCapture(capture)
	}
}

func (topUp *TopUp) PayPalProtectionNotificationLine() string {
	if topUp.PaymentProvider != PaymentProviderPayPal {
		return ""
	}
	label := "未知（PayPal 未返回保障信息）"
	switch topUp.PayPalSellerProtection.Status {
	case "ELIGIBLE":
		label = "符合资格"
	case "PARTIALLY_ELIGIBLE":
		label = "部分符合资格"
	case "NOT_ELIGIBLE":
		label = "不符合资格"
	}
	var categories []string
	if topUp.PayPalSellerProtection.Status == "ELIGIBLE" || topUp.PayPalSellerProtection.Status == "PARTIALLY_ELIGIBLE" {
		for _, category := range topUp.PayPalSellerProtection.DisputeCategories {
			switch category {
			case "ITEM_NOT_RECEIVED":
				categories = append(categories, "未收到商品")
			case "UNAUTHORIZED_TRANSACTION":
				categories = append(categories, "未经授权交易")
			}
		}
	}
	if len(categories) > 0 {
		label += "（" + strings.Join(categories, "、") + "）"
	}
	return "PayPal 卖家保障：" + label
}
