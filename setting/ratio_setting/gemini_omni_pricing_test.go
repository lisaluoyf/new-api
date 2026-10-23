package ratio_setting

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestGeminiOmniPricingConfiguration(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	before, had := common.OptionMap[GeminiOmniPricingOption]
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	delete(common.OptionMap, GeminiOmniPricingOption)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if had {
			common.OptionMap[GeminiOmniPricingOption] = before
		} else {
			delete(common.OptionMap, GeminiOmniPricingOption)
		}
	})
	require.NoError(t, ValidateGeminiOmniPricing(DefaultGeminiOmniPricingJSON()))
	require.InDelta(t, .264, GetGeminiOmniPrice("4k", 1, false, false), 1e-12)
	require.InDelta(t, .175, GetGeminiOmniPrice("360p", 6, true, false), 1e-12)
	require.InDelta(t, .8, GetGeminiOmniPrice("720p", 10, true, true), 1e-12)
	var values map[string]float64
	require.NoError(t, common.UnmarshalJsonStr(DefaultGeminiOmniPricingJSON(), &values))
	values["Official-4K"] = .3
	values["Ext-720P-6s"] = .06
	raw, err := common.Marshal(values)
	require.NoError(t, err)
	common.OptionMapRWMutex.Lock()
	common.OptionMap[GeminiOmniPricingOption] = string(raw)
	common.OptionMapRWMutex.Unlock()
	require.InDelta(t, .6, GetGeminiOmniPrice("4k", 2, false, false), 1e-12)
	require.InDelta(t, .36, GetGeminiOmniPrice("720p", 6, true, false), 1e-12)
	delete(values, "Ext-720P-6s")
	raw, _ = common.Marshal(values)
	require.Error(t, ValidateGeminiOmniPricing(string(raw)))
	values["Ext-720P-6s"] = -1
	raw, _ = common.Marshal(values)
	require.Error(t, ValidateGeminiOmniPricing(string(raw)))
	values["Ext-720P-6s"] = .05
	values["wrong"] = 1
	raw, _ = common.Marshal(values)
	require.Error(t, ValidateGeminiOmniPricing(string(raw)))
	require.Error(t, ValidateGeminiOmniPricing(`null`))
}
