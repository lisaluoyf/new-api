package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestGetChargedAmountWithTierDiscount(t *testing.T) {
	originalDiscounts := operation_setting.GetPaymentSetting().AmountDiscount
	t.Cleanup(func() {
		operation_setting.GetPaymentSetting().AmountDiscount = originalDiscounts
	})

	user := model.User{Group: "default"}

	operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{
		50: 0.9,
	}
	require.InDelta(t, 45.0, GetChargedAmountWithTierDiscount(50, user), 0.000001)
	require.InDelta(t, 10.0, GetChargedAmountWithTierDiscount(10, user), 0.000001)

	operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{
		50: 0,
	}
	require.InDelta(t, 50.0, GetChargedAmountWithTierDiscount(50, user), 0.000001)
}
