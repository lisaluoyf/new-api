package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPlategaCallbackPayloadAcceptsNumericPaymentMethod(t *testing.T) {
	raw := `{
		"transactionId": "tx-1",
		"status": "CONFIRMED",
		"payload": "PLATEGA-1",
		"amount": 1.09,
		"currency": "RUB",
		"paymentMethod": 2
	}`
	var payload PlategaCallbackPayload
	require.NoError(t, json.Unmarshal([]byte(raw), &payload))
	require.Equal(t, "tx-1", payload.TransactionId)
	require.Equal(t, "CONFIRMED", payload.Status)
}

func TestPlategaAmountsMatchDoesNotInventFee(t *testing.T) {
	require.False(t, PlategaAmountsMatch(1.0, 1.09))
	require.False(t, PlategaAmountsMatch(90.0, 97.65))
	require.True(t, PlategaAmountsMatch(97.65, 97.65))
	require.False(t, PlategaAmountsMatch(90.0, 50.0))
}
