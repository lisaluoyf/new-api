package operation_setting

import (
	"time"

	"github.com/QuantumNous/new-api/setting/config"
)

type PaymentSetting struct {
	AmountOptions           []int           `json:"amount_options"`
	AmountDiscount          map[int]float64 `json:"amount_discount"`            // 充值金额对应的折扣，例如 100 元 0.9 表示 100 元充值享受 9 折优惠
	AmountDiscountExpiresAt map[int]int64   `json:"amount_discount_expires_at"` // Unix timestamp; absent or 0 means no expiry.
}

// 默认配置
var paymentSetting = PaymentSetting{
	AmountOptions:           []int{10, 20, 50, 100, 200, 500},
	AmountDiscount:          map[int]float64{},
	AmountDiscountExpiresAt: map[int]int64{},
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("payment_setting", &paymentSetting)
}

func GetPaymentSetting() *PaymentSetting {
	return &paymentSetting
}

// GetActiveAmountDiscount returns a configured tier discount only while its
// optional campaign deadline has not passed. The expiry is exclusive so a
// campaign ending at 2026-09-15 23:59:59 stops precisely at the next second.
func GetActiveAmountDiscount(amount int, now time.Time) float64 {
	if amount <= 0 {
		return 1
	}
	discount, ok := paymentSetting.AmountDiscount[amount]
	if !ok || discount <= 0 || discount >= 1 {
		return 1
	}
	if expiresAt := paymentSetting.AmountDiscountExpiresAt[amount]; expiresAt > 0 && now.Unix() > expiresAt {
		return 1
	}
	return discount
}

// ActiveAmountDiscounts is the public catalog for the wallet UI. Expired
// campaign tiers are intentionally omitted so a stale client cannot advertise
// a price that the server will no longer charge.
func ActiveAmountDiscounts(now time.Time) map[int]float64 {
	active := make(map[int]float64)
	for amount := range paymentSetting.AmountDiscount {
		if discount := GetActiveAmountDiscount(amount, now); discount < 1 {
			active[amount] = discount
		}
	}
	return active
}

func ActiveAmountDiscountExpiries(now time.Time) map[int]int64 {
	active := make(map[int]int64)
	for amount, expiresAt := range paymentSetting.AmountDiscountExpiresAt {
		if expiresAt > 0 && GetActiveAmountDiscount(amount, now) < 1 {
			active[amount] = expiresAt
		}
	}
	return active
}
