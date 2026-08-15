package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestCalculateSubscriptionEpayChargeAmountConvertsUSDToCNY(t *testing.T) {
	amount, err := calculateSubscriptionEpayChargeAmount(10, 7.3)
	require.NoError(t, err)
	require.Equal(t, "73.00", amount)

	amount, err = calculateSubscriptionEpayChargeAmount(2.345, 7.3)
	require.NoError(t, err)
	require.Equal(t, "17.12", amount)
}

func TestCalculateSubscriptionEpayChargeAmountRejectsInvalidRate(t *testing.T) {
	_, err := calculateSubscriptionEpayChargeAmount(10, 0)
	require.Error(t, err)
}

func TestVerifySubscriptionEpayPaymentSnapshot(t *testing.T) {
	snapshot := common.GetJsonString(subscriptionEpayPaymentSnapshot{
		PayableUSD:     10,
		ExchangeRate:   7.3,
		ChargeAmount:   "73.00",
		ChargeCurrency: "CNY",
	})
	require.NoError(t, verifySubscriptionEpayPaymentSnapshot(snapshot, "73"))
	require.NoError(t, verifySubscriptionEpayPaymentSnapshot(snapshot, "73.00"))
	require.ErrorContains(t, verifySubscriptionEpayPaymentSnapshot(snapshot, "10.00"), "amount mismatch")
	require.Error(t, verifySubscriptionEpayPaymentSnapshot("", "73.00"))
}
