package controller

import (
	"github.com/QuantumNous/new-api/setting"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCreemNeverBypassesAuthenticationInTestMode(t *testing.T) {
	old := setting.CreemTestMode
	t.Cleanup(func() { setting.CreemTestMode = old })
	for _, mode := range []bool{false, true} {
		setting.CreemTestMode = mode
		require.False(t, verifyCreemSignature(`{"eventType":"checkout.completed"}`, "forged", ""))
	}
	payload := `{"eventType":"checkout.completed"}`
	require.True(t, verifyCreemSignature(payload, generateCreemSignature(payload, "test-secret"), "test-secret"))
	require.False(t, verifyCreemSignature(payload, "forged", "test-secret"))
}
