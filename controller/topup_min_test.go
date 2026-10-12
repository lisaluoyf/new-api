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

func TestEffectiveWalletMinTopupUSD(t *testing.T) {
	testCases := []struct {
		name               string
		configuredMin      int
		hasSuccessfulTopup bool
		expectedMin        int
	}{
		{name: "first payment keeps one dollar minimum", configuredMin: 1, expectedMin: 1},
		{name: "first payment preserves configured minimum", configuredMin: 20, expectedMin: 20},
		{name: "repeat payment raises one dollar minimum", configuredMin: 1, hasSuccessfulTopup: true, expectedMin: 10},
		{name: "repeat payment raises five dollar minimum", configuredMin: 5, hasSuccessfulTopup: true, expectedMin: 10},
		{name: "repeat payment keeps ten dollar minimum", configuredMin: 10, hasSuccessfulTopup: true, expectedMin: 10},
		{name: "repeat payment preserves higher minimum", configuredMin: 20, hasSuccessfulTopup: true, expectedMin: 20},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.expectedMin, effectiveWalletMinTopupUSD(testCase.configuredMin, testCase.hasSuccessfulTopup))
		})
	}
}

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

	require.EqualValues(t, 10, getWalletMinTopupForUser(1, 1))
	require.EqualValues(t, 10, getWalletMinTopupForUser(1, 10))
	require.EqualValues(t, 20, getWalletMinTopupForUser(1, 20))
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

	require.EqualValues(t, 5000000, getWalletMinTopupForUser(1, 1))
}

func TestClonePayMethodsWithMinTopupReturnsNumericMinimum(t *testing.T) {
	original := []map[string]string{
		{"name": "Alipay", "type": "alipay"},
		{"name": "WeChat Pay", "type": "wxpay"},
	}

	cloned := clonePayMethodsWithMinTopup(original, map[string]int64{
		"alipay": 10,
		"wxpay":  10,
	})

	require.Len(t, cloned, 2)
	require.IsType(t, int64(0), cloned[0]["min_topup"])
	require.EqualValues(t, 10, cloned[0]["min_topup"])
	require.IsType(t, int64(0), cloned[1]["min_topup"])
	require.EqualValues(t, 10, cloned[1]["min_topup"])
	require.NotContains(t, original[0], "min_topup")
}
