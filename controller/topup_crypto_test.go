package controller

import (
	"testing"

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
