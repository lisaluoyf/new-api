package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTopupInvoiceOriginalPaymentAmounts(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&TopUp{}, &SubscriptionOrder{}, &PlategaOrder{}))
	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous })
	for _, test := range []struct {
		name, method, provider, payload, want string
		money                                 float64
		subscription                          bool
		rub                                   float64
		unavailable                           bool
	}{
		{name: "discount", method: "stripe", money: 8.5, want: "USD 8.50"},
		{name: "wallet CNY", method: "alipay", money: 60, want: "CNY 60.00"},
		{name: "wallet RUB", method: "platega", money: 800, want: "RUB 800.00"},
		{name: "subscription USD", method: "stripe", money: 20, subscription: true, want: "USD 20.00"},
		{name: "subscription CNY", method: "wxpay", provider: "epay", money: 10, subscription: true,
			payload: `{"payment_snapshot":{"charge_amount":"73.00","charge_currency":"CNY"}}`, want: "CNY 73.00"},
		{name: "subscription RUB", method: "platega", provider: "platega", money: 10, subscription: true, rub: 835.5, want: "RUB 835.50"},
		{name: "missing CNY snapshot", method: "wxpay", provider: "epay", money: 10, subscription: true, unavailable: true},
		{name: "missing currency", method: "wxpay", provider: "epay", money: 10, subscription: true, payload: `{"charge_amount":"73.00"}`, unavailable: true},
		{name: "snapshot EUR", method: "alipay", provider: "epay", money: 10, subscription: true, payload: `{"charge_amount":"9.50","charge_currency":"EUR"}`, want: "EUR 9.50"},
		{name: "missing RUB snapshot", method: "platega", provider: "platega", money: 10, subscription: true, unavailable: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			topup := TopUp{TradeNo: test.name, UserId: 7, Money: test.money, Amount: 100, CreditedAmount: 100,
				PaymentMethod: test.method, Status: common.TopUpStatusSuccess, CompleteTime: 1780000000}
			require.NoError(t, db.Create(&topup).Error)
			if test.subscription {
				require.NoError(t, db.Create(&SubscriptionOrder{UserId: 7, TradeNo: test.name, PaymentProvider: test.provider, ProviderPayload: test.payload}).Error)
			}
			if test.rub > 0 {
				require.NoError(t, db.Create(&PlategaOrder{TradeNo: test.name, RubAmount: test.rub}).Error)
			}
			invoice, err := GetTopupInvoice(topup.Id, 7, false)
			if test.unavailable {
				require.ErrorIs(t, err, ErrInvoiceUnavailable)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.want, invoice.AmountPaid)
			require.Equal(t, topup.CompleteTime, invoice.PaidAt)
			if test.subscription {
				require.Equal(t, "API subscription", invoice.Description)
			}
		})
	}
}
