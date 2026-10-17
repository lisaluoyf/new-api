package model

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestVeoPricingMetadataMatchesSecondBasedBilling(t *testing.T) {
	old := common.OptionMap
	t.Cleanup(func() { common.OptionMap = old })
	common.OptionMap = map[string]string{}
	p := Pricing{}
	require.True(t, applyVeo31Pricing(&p, "veo-3.1-fast-generate-preview", .1))
	require.Equal(t, "second", p.BillingUnit)
	require.Equal(t, 1, p.QuotaType)
	require.Equal(t, []constant.EndpointType{constant.EndpointTypeOpenAIVideo}, p.SupportedEndpointTypes)
	require.Len(t, p.ResolutionPrices, 6)
	require.InDelta(t, .3, p.ResolutionPrices["4K"], 1e-9)
	require.InDelta(t, .08, p.ResolutionPrices["720P-silent"], 1e-9)
	require.True(t, applyVeo31Pricing(&p, "veo-3.1-fast-generate-preview", .2))
	require.InDelta(t, .6, p.ResolutionPrices["4K"], 1e-9)
	common.OptionMap["VideoModelPricing"] = `{"veo-3.1-generate-preview":{"prices":{"720p":0.4,"4k":0.7}}}`
	require.True(t, applyVeo31Pricing(&p, "veo-3.1-generate-preview", .4))
	require.InDelta(t, .7, p.ResolutionPrices["4K"], 1e-9)
	before := p
	require.False(t, applyVeo31Pricing(&p, "gpt-6-astra", 1))
	require.Equal(t, before, p)
}
