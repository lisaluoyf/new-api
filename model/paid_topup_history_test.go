package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestHasSuccessfulPaidTopUpCountsOnlyRealPaidRecords(t *testing.T) {
	oldDB := DB
	t.Cleanup(func() {
		DB = oldDB
	})

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&TopUp{}, &SubscriptionOrder{}))
	DB = db

	require.False(t, HasSuccessfulPaidTopUp(1))

	require.NoError(t, db.Create(&[]TopUp{
		{
			UserId:          1,
			TradeNo:         "pending-wallet",
			PaymentMethod:   PaymentMethodPayPal,
			PaymentProvider: PaymentProviderPayPal,
			Money:           10,
			Status:          common.TopUpStatusPending,
		},
		{
			UserId:          1,
			TradeNo:         "free-credit",
			PaymentMethod:   PaymentMethodFree,
			PaymentProvider: PaymentProviderFree,
			Amount:          50,
			Status:          common.TopUpStatusSuccess,
		},
		{
			UserId:              1,
			TradeNo:             "free-trial-subscription",
			PaymentMethod:       PaymentMethodStripe,
			PaymentProvider:     PaymentProviderStripe,
			PaidAmountUSDSource: "subscription",
			Status:              common.TopUpStatusSuccess,
		},
	}).Error)
	require.False(t, HasSuccessfulPaidTopUp(1))

	require.NoError(t, db.Create(&TopUp{
		UserId:              1,
		TradeNo:             "paid-subscription",
		PaymentMethod:       PaymentMethodPayPal,
		PaymentProvider:     PaymentProviderPayPal,
		PaidAmountUSD:       9.99,
		PaidAmountUSDSource: "subscription",
		Money:               9.99,
		Status:              common.TopUpStatusSuccess,
	}).Error)
	require.True(t, HasSuccessfulPaidTopUp(1))
}

func TestHasSuccessfulPaidTopUpCountsWalletPayments(t *testing.T) {
	oldDB := DB
	t.Cleanup(func() {
		DB = oldDB
	})

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&TopUp{}))
	DB = db

	require.NoError(t, db.Create(&TopUp{
		UserId:          2,
		TradeNo:         "legacy-epay",
		PaymentMethod:   "alipay",
		PaymentProvider: PaymentProviderEpay,
		Amount:          1,
		Money:           1,
		Status:          common.TopUpStatusSuccess,
	}).Error)

	require.True(t, HasSuccessfulPaidTopUp(2))
	require.False(t, HasSuccessfulPaidTopUp(3))
}
