package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionStripePaymentSnapshot(t *testing.T) {
	snapshot, err := newSubscriptionStripePaymentSnapshot(10, "usd")
	require.NoError(t, err)
	require.EqualValues(t, 1000, snapshot.ChargeAmount)
	require.Equal(t, "USD", snapshot.ChargeCurrency)

	snapshotJSON := common.GetJsonString(snapshot)
	require.NoError(t, verifySubscriptionStripePaymentSnapshot(snapshotJSON, "1000", "usd"))
	require.ErrorContains(t, verifySubscriptionStripePaymentSnapshot(snapshotJSON, "100", "usd"), "mismatch")
	require.ErrorContains(t, verifySubscriptionStripePaymentSnapshot(snapshotJSON, "1000", "cny"), "mismatch")
}

func TestSubscriptionStripePaymentSnapshotRejectsUnsupportedCharge(t *testing.T) {
	_, err := newSubscriptionStripePaymentSnapshot(0.49, "USD")
	require.Error(t, err)

	_, err = newSubscriptionStripePaymentSnapshot(10, "CNY")
	require.Error(t, err)
}
