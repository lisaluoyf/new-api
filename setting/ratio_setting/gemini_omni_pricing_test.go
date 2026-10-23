package ratio_setting

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestOmniOfficialVideoPricingValidation(t *testing.T) {
	prices := map[string]float64{"360P": .0337925, "720P": .10136, "1080P": .15204, "4K": .30408}
	entry := map[string]any{"unit": "second", "base_variant": "720P", "base_price": .10136, "prices": prices, "official_prices": prices}
	raw, err := common.Marshal(map[string]any{"gemini-omni-1.1-flash": entry, "other": map[string]any{"prices": map[string]float64{"720P": 1}}})
	require.NoError(t, err)
	require.NoError(t, ValidateOmniOfficialVideoPricing(string(raw)))
	require.NoError(t, ValidateOmniOfficialVideoPricing(`{"other":{"prices":{"720P":1}}}`))
	require.Error(t, ValidateOmniOfficialVideoPricing(`{"gemini-omni-1.1-flash":{"prices":{"720P":.10136}}}`))
	entry["base_price"] = .2
	raw, _ = common.Marshal(map[string]any{"gemini-omni-1.1-flash": entry})
	require.Error(t, ValidateOmniOfficialVideoPricing(string(raw)))
	require.Error(t, ValidateOmniOfficialVideoPricing(`null`))
}
