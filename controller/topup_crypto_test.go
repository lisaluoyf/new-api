package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestMatchCryptoAmountDiscountTier(t *testing.T) {
	originalDiscounts := operation_setting.GetPaymentSetting().AmountDiscount
	t.Cleanup(func() {
		operation_setting.GetPaymentSetting().AmountDiscount = originalDiscounts
	})

	testCases := []struct {
		name             string
		discounts        map[int]float64
		paid             float64
		expectedTier     int
		expectedDiscount float64
		applied          bool
	}{
		{
			name:             "no discount config keeps credited amount unchanged",
			discounts:        map[int]float64{},
			paid:             45,
			expectedTier:     0,
			expectedDiscount: 0,
			applied:          false,
		},
		{
			name:             "hitting configured fifty tier matches that tier",
			discounts:        map[int]float64{50: 0.9},
			paid:             45,
			expectedTier:     50,
			expectedDiscount: 0.9,
			applied:          true,
		},
		{
			name:             "larger crypto payments still use the matched tier discount factor",
			discounts:        map[int]float64{50: 0.9},
			paid:             100,
			expectedTier:     50,
			expectedDiscount: 0.9,
			applied:          true,
		},
		{
			name:             "payment below threshold does not trigger inflation",
			discounts:        map[int]float64{50: 0.9},
			paid:             44.99,
			expectedTier:     0,
			expectedDiscount: 0,
			applied:          false,
		},
		{
			name:             "invalid discounts are ignored",
			discounts:        map[int]float64{50: 1, 100: 0, 200: -0.5},
			paid:             100,
			expectedTier:     0,
			expectedDiscount: 0,
			applied:          false,
		},
		{
			name:             "highest eligible tier wins when multiple tiers are configured",
			discounts:        map[int]float64{50: 0.9, 100: 0.95},
			paid:             100,
			expectedTier:     100,
			expectedDiscount: 0.95,
			applied:          true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			operation_setting.GetPaymentSetting().AmountDiscount = tc.discounts
			actualTier, actualDiscount, applied := matchCryptoAmountDiscountTier(tc.paid)
			require.Equal(t, tc.applied, applied)
			require.Equal(t, tc.expectedTier, actualTier)
			require.InDelta(t, tc.expectedDiscount, actualDiscount, 0.000001)
		})
	}
}

func TestCryptoFirstTopupPromoMinPaidUSD(t *testing.T) {
	originalAmount := common.FirstTopupPromoAmount
	originalDiscount := common.FirstTopupPromoDiscount
	t.Cleanup(func() {
		common.FirstTopupPromoAmount = originalAmount
		common.FirstTopupPromoDiscount = originalDiscount
	})

	common.FirstTopupPromoAmount = 10
	common.FirstTopupPromoDiscount = 0.75
	require.InDelta(t, 7.0, cryptoFirstTopupPromoMinPaidUSD(), 0.000001)
}

func TestApplyCryptoFirstTopupPromoRequiresMinPaidThreshold(t *testing.T) {
	originalEnabled := common.FirstTopupPromoEnabled
	originalAmount := common.FirstTopupPromoAmount
	originalDiscount := common.FirstTopupPromoDiscount
	t.Cleanup(func() {
		common.FirstTopupPromoEnabled = originalEnabled
		common.FirstTopupPromoAmount = originalAmount
		common.FirstTopupPromoDiscount = originalDiscount
	})

	common.FirstTopupPromoEnabled = true
	common.FirstTopupPromoAmount = 10
	common.FirstTopupPromoDiscount = 0.75

	testCases := []struct {
		name           string
		paid           float64
		expectedCredit float64
		expectedBonus  float64
		applied        bool
	}{
		{
			name:           "below threshold does not inflate",
			paid:           6.99,
			expectedCredit: 6.99,
			expectedBonus:  0,
			applied:        false,
		},
		{
			name:           "seven dollars starts promo",
			paid:           7,
			expectedCredit: 7 / 0.75,
			expectedBonus:  7/0.75 - 7,
			applied:        true,
		},
		{
			name:           "seven point five still credits ten",
			paid:           7.5,
			expectedCredit: 10,
			expectedBonus:  2.5,
			applied:        true,
		},
		{
			name:           "bonus remains capped by configured promo amount",
			paid:           20,
			expectedCredit: 20 + 10*(1/0.75-1),
			expectedBonus:  10 * (1/0.75 - 1),
			applied:        true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualCredit, actualBonus, applied := applyCryptoFirstTopupPromo(tc.paid)
			require.Equal(t, tc.applied, applied)
			require.InDelta(t, tc.expectedCredit, actualCredit, 0.000001)
			require.InDelta(t, tc.expectedBonus, actualBonus, 0.000001)
		})
	}
}
