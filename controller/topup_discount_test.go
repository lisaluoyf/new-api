package controller

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestGetChargedAmountWithTierDiscount(t *testing.T) {
	originalDiscounts := operation_setting.GetPaymentSetting().AmountDiscount
	originalExpiries := operation_setting.GetPaymentSetting().AmountDiscountExpiresAt
	t.Cleanup(func() {
		operation_setting.GetPaymentSetting().AmountDiscount = originalDiscounts
		operation_setting.GetPaymentSetting().AmountDiscountExpiresAt = originalExpiries
	})

	user := model.User{Group: "default"}

	operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{
		50: 0.9,
	}
	operation_setting.GetPaymentSetting().AmountDiscountExpiresAt = map[int]int64{}
	require.InDelta(t, 45.0, GetChargedAmountWithTierDiscount(50, user), 0.000001)
	require.InDelta(t, 10.0, GetChargedAmountWithTierDiscount(10, user), 0.000001)

	operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{
		50: 0,
	}
	require.InDelta(t, 50.0, GetChargedAmountWithTierDiscount(50, user), 0.000001)
}

func TestAmountDiscountExpiresAtDeadline(t *testing.T) {
	originalDiscounts := operation_setting.GetPaymentSetting().AmountDiscount
	originalExpiries := operation_setting.GetPaymentSetting().AmountDiscountExpiresAt
	t.Cleanup(func() {
		operation_setting.GetPaymentSetting().AmountDiscount = originalDiscounts
		operation_setting.GetPaymentSetting().AmountDiscountExpiresAt = originalExpiries
	})

	now := time.Date(2026, time.September, 15, 15, 59, 59, 0, time.UTC)
	operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{50: 0.9}
	operation_setting.GetPaymentSetting().AmountDiscountExpiresAt = map[int]int64{50: now.Unix()}

	require.InDelta(t, 0.9, operation_setting.GetActiveAmountDiscount(50, now), 0.000001)
	require.Equal(t, map[int]float64{50: 0.9}, operation_setting.ActiveAmountDiscounts(now))
	require.InDelta(t, 1.0, operation_setting.GetActiveAmountDiscount(50, now.Add(time.Second)), 0.000001)
	require.Empty(t, operation_setting.ActiveAmountDiscounts(now.Add(time.Second)))
}
