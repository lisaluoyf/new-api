package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFormatWaffoPancakeAmount_UsesDisplayPriceString(t *testing.T) {
	testCases := []struct {
		name     string
		amount   float64
		expected string
	}{
		{name: "whole amount", amount: 29, expected: "29.00"},
		{name: "decimal amount", amount: 29.9, expected: "29.90"},
		{name: "round half up to cents", amount: 29.999, expected: "30.00"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected, formatWaffoPancakeAmount(tc.amount))
		})
	}
}

func TestGetWaffoPancakePayMoney(t *testing.T) {
	originalPromoEnabled := common.FirstTopupPromoEnabled
	originalUnitPrice := setting.WaffoPancakeUnitPrice
	originalQuotaDisplayType := operation_setting.GetGeneralSetting().QuotaDisplayType
	originalDiscounts := make(map[int]float64, len(operation_setting.GetPaymentSetting().AmountDiscount))
	for k, v := range operation_setting.GetPaymentSetting().AmountDiscount {
		originalDiscounts[k] = v
	}
	originalTopupGroupRatio := common.TopupGroupRatio2JSONString()

	t.Cleanup(func() {
		common.FirstTopupPromoEnabled = originalPromoEnabled
		setting.WaffoPancakeUnitPrice = originalUnitPrice
		operation_setting.GetGeneralSetting().QuotaDisplayType = originalQuotaDisplayType
		operation_setting.GetPaymentSetting().AmountDiscount = originalDiscounts
		require.NoError(t, common.UpdateTopupGroupRatioByJSONString(originalTopupGroupRatio))
	})

	common.FirstTopupPromoEnabled = false
	setting.WaffoPancakeUnitPrice = 2.5
	operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{
		10:                           0.8,
		int(common.QuotaPerUnit * 3): 0.5,
		20:                           0,
	}
	require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":1,"vip":1.2}`))

	testCases := []struct {
		name             string
		amount           int64
		group            string
		quotaDisplayType string
		expected         float64
	}{
		{
			name:             "currency display applies unit price group ratio and discount",
			amount:           10,
			group:            "vip",
			quotaDisplayType: operation_setting.QuotaDisplayTypeUSD,
			expected:         24,
		},
		{
			name:             "tokens display converts quota to display units before pricing",
			amount:           int64(common.QuotaPerUnit * 3),
			group:            "vip",
			quotaDisplayType: operation_setting.QuotaDisplayTypeTokens,
			expected:         4.5,
		},
		{
			name:             "non-positive discount falls back to no discount",
			amount:           20,
			group:            "default",
			quotaDisplayType: operation_setting.QuotaDisplayTypeUSD,
			expected:         50,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			operation_setting.GetGeneralSetting().QuotaDisplayType = tc.quotaDisplayType
			actual := getWaffoPancakePayMoney(tc.amount, tc.group, 0)
			require.InDelta(t, tc.expected, actual, 0.000001)
		})
	}
}

func TestGetWaffoPancakePayMoneyAppliesFirstTopupPromoOnlyOnce(t *testing.T) {
	oldDB := model.DB
	oldEnabled := common.FirstTopupPromoEnabled
	oldDiscount := common.FirstTopupPromoDiscount
	oldAmount := common.FirstTopupPromoAmount
	oldWindow := common.FirstTopupPromoWindowDays
	oldPrice := setting.WaffoPancakeUnitPrice
	oldDisplay := operation_setting.GetGeneralSetting().QuotaDisplayType
	oldDiscounts := operation_setting.GetPaymentSetting().AmountDiscount
	t.Cleanup(func() {
		model.DB = oldDB
		common.FirstTopupPromoEnabled = oldEnabled
		common.FirstTopupPromoDiscount = oldDiscount
		common.FirstTopupPromoAmount = oldAmount
		common.FirstTopupPromoWindowDays = oldWindow
		setting.WaffoPancakeUnitPrice = oldPrice
		operation_setting.GetGeneralSetting().QuotaDisplayType = oldDisplay
		operation_setting.GetPaymentSetting().AmountDiscount = oldDiscounts
	})

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}))
	model.DB = db
	common.FirstTopupPromoEnabled = true
	common.FirstTopupPromoDiscount = 0.75
	common.FirstTopupPromoAmount = 10
	common.FirstTopupPromoWindowDays = 3
	setting.WaffoPancakeUnitPrice = 1
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeUSD
	operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{}
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "waffo-promo-user", CreatedAt: common.GetTimestamp() - 3600}).Error)

	require.InDelta(t, 7.5, getWaffoPancakePayMoney(10, "default", 1), 0.000001)
	require.InDelta(t, 20.0, getWaffoPancakePayMoney(20, "default", 1), 0.000001)
	require.NoError(t, db.Create(&model.TopUp{
		UserId: 1, Amount: 10, PaidAmountUSD: 7.5, Money: 7.5, TradeNo: "waffo-promo-success",
		PaymentMethod: model.PaymentMethodWaffoPancake, PaymentProvider: model.PaymentProviderWaffoPancake,
		Status: common.TopUpStatusSuccess,
	}).Error)
	require.InDelta(t, 10.0, getWaffoPancakePayMoney(10, "default", 1), 0.000001)
}
