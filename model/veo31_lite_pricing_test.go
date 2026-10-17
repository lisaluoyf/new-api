package model

import (
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestVeoLitePricingMetadata(t *testing.T) {
	var pricing Pricing
	require.True(t, applyVeo31Pricing(&pricing, "veo-3.1-lite-generate-preview", .05))
	require.Equal(t, map[string]float64{"720P": .05, "1080P": .08}, pricing.ResolutionPrices)
	require.Equal(t, "second", pricing.BillingUnit)
	require.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAIVideo}, pricing.SupportedEndpointTypes)
}
