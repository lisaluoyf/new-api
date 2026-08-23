package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWalletMinTopupForUserUsesPaidHistory(t *testing.T) {
	oldDB := model.DB
	t.Cleanup(func() {
		model.DB = oldDB
	})

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.TopUp{}))
	model.DB = db

	require.EqualValues(t, 1, getWalletMinTopupForUser(1, 1))

	require.NoError(t, db.Create(&model.TopUp{
		UserId:          1,
		TradeNo:         "paid-wallet",
		PaymentMethod:   model.PaymentMethodPayPal,
		PaymentProvider: model.PaymentProviderPayPal,
		PaidAmountUSD:   1,
		Money:           1,
		Status:          common.TopUpStatusSuccess,
	}).Error)

	require.EqualValues(t, 5, getWalletMinTopupForUser(1, 1))
	require.EqualValues(t, 10, getWalletMinTopupForUser(1, 10))
}

func TestWalletMinTopupForUserConvertsTokenDisplay(t *testing.T) {
	oldDB := model.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.TopUp{}))
	model.DB = db

	oldDisplay := operation_setting.GetGeneralSetting().QuotaDisplayType
	oldQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() {
		model.DB = oldDB
		operation_setting.GetGeneralSetting().QuotaDisplayType = oldDisplay
		common.QuotaPerUnit = oldQuotaPerUnit
	})

	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeTokens
	common.QuotaPerUnit = 500000

	require.EqualValues(t, 500000, getWalletMinTopupForUser(1, 1))

	require.NoError(t, db.Create(&model.TopUp{
		UserId:          1,
		TradeNo:         "paid-wallet",
		PaymentMethod:   model.PaymentMethodCrypto,
		PaymentProvider: model.PaymentProviderCrypto,
		PaidAmountUSD:   1,
		Money:           1,
		Status:          common.TopUpStatusSuccess,
	}).Error)

	require.EqualValues(t, 2500000, getWalletMinTopupForUser(1, 1))
}
