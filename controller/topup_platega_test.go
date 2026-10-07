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

func TestPlategaFirstTopupPromoOnlyAppliesToEligibleTier(t *testing.T) {
	oldDB := model.DB
	oldEnabled := common.FirstTopupPromoEnabled
	oldDiscount := common.FirstTopupPromoDiscount
	oldAmount := common.FirstTopupPromoAmount
	oldWindow := common.FirstTopupPromoWindowDays
	oldRate := setting.PlategaUSDRate
	oldPaymentSetting := operation_setting.GetPaymentSetting().AmountDiscount
	t.Cleanup(func() {
		model.DB = oldDB
		common.FirstTopupPromoEnabled = oldEnabled
		common.FirstTopupPromoDiscount = oldDiscount
		common.FirstTopupPromoAmount = oldAmount
		common.FirstTopupPromoWindowDays = oldWindow
		setting.PlategaUSDRate = oldRate
		operation_setting.GetPaymentSetting().AmountDiscount = oldPaymentSetting
	})

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}))
	model.DB = db

	common.FirstTopupPromoEnabled = true
	common.FirstTopupPromoDiscount = 0.85
	common.FirstTopupPromoAmount = 10
	common.FirstTopupPromoWindowDays = 3
	setting.PlategaUSDRate = 83.55
	operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{}
	now := common.GetTimestamp()
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "promo-user", CreatedAt: now - 3600}).Error)

	// Round promotional prices only after applying the discount and RUB rate.
	for _, tc := range []struct {
		discount float64
		wantRUB  float64
	}{
		{0.85, 710.18}, // 710.175: round half up
		{0.75, 626.63}, // 626.625: round half up
		{0.84, 701.82}, // already two decimals
		{0.83, 693.47}, // 693.465: round half up
		{0.82, 685.11}, // already two decimals
	} {
		common.FirstTopupPromoDiscount = tc.discount
		require.Equal(t, tc.wantRUB, getPlategaPayRubAmount(10, "default", 1))
	}
	common.FirstTopupPromoDiscount = 0.85
	// The promo is tier-specific; a different amount remains full price.
	require.InDelta(t, 20*83.55, getPlategaPayRubAmount(20, "default", 1), 0.0001)

	// Once any successful top-up exists, the same $10 tier is full price too.
	require.NoError(t, db.Create(&model.TopUp{
		UserId: 1, Amount: 10, PaidAmountUSD: 8.5, Money: 8.5, TradeNo: "promo-test-success",
		PaymentMethod: model.PaymentMethodPlatega, PaymentProvider: model.PaymentProviderPlatega,
		Status: common.TopUpStatusSuccess,
	}).Error)
	require.InDelta(t, 10*83.55, getPlategaPayRubAmount(10, "default", 1), 0.0001)
}
