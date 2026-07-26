package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSuccessfulTopupUSDTotalDoesNotMixLocalCurrencies(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&TopUp{}))
	DB = db

	topups := []TopUp{
		{
			UserId:          42,
			Amount:          10,
			Money:           52.5, // CNY charged through Alipay.
			Status:          common.TopUpStatusSuccess,
			PaymentMethod:   "alipay",
			PaymentProvider: PaymentProviderEpay,
			TradeNo:         "success-local-1",
		},
		{
			UserId:          42,
			Amount:          10,
			Money:           70, // CNY charged through Alipay.
			Status:          common.TopUpStatusSuccess,
			PaymentMethod:   "alipay",
			PaymentProvider: PaymentProviderEpay,
			TradeNo:         "success-local-2",
		},
		{
			UserId:          42,
			Amount:          12,
			CreditedAmount:  12.5,
			Money:           999,
			Status:          common.TopUpStatusSuccess,
			PaymentProvider: "crypto",
			TradeNo:         "success-precise",
		},
		{
			UserId:  42,
			Amount:  100,
			Money:   700,
			Status:  common.TopUpStatusPending,
			TradeNo: "pending",
		},
		{
			UserId:  99,
			Amount:  1000,
			Money:   7000,
			Status:  common.TopUpStatusSuccess,
			TradeNo: "other-user",
		},
	}
	require.NoError(t, db.Create(&topups).Error)

	total, err := successfulTopupUSDTotal(42)
	require.NoError(t, err)
	require.InDelta(t, 32.5, total, 0.001)
}
