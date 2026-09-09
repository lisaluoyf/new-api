package ratio_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/require"
)

func TestDefaultImageModelPricingIncludesResolutionPrices(t *testing.T) {
	base, ok := GetImageModelBasePrice("gpt-image-2")
	require.True(t, ok)
	require.InDelta(t, 0.25, base, 1e-9)

	tests := map[string]float64{
		"1K": 0.25,
		"2k": 0.30,
		"4K": 0.60,
	}
	for resolution, want := range tests {
		got, found := GetImageModelPrice("gpt-image-2", resolution)
		require.True(t, found, resolution)
		require.InDelta(t, want, got, 1e-9, resolution)
		require.InDelta(t, want/base, GetImageModelPriceRatio("gpt-image-2", resolution), 1e-9, resolution)
	}

	details, found := GetImageModelPricingDetails("GPT-IMAGE-2")
	require.True(t, found)
	require.Equal(t, "image", details.Unit)
	require.Equal(t, "1K", details.BaseVariant)

	geminiTests := map[string]struct {
		base   float64
		prices map[string]float64
	}{
		"gemini-2.5-flash-image": {
			base:   0.039,
			prices: map[string]float64{"1K": 0.039},
		},
		"gemini-3-pro-image": {
			base:   0.134,
			prices: map[string]float64{"1K": 0.134, "2K": 0.134, "4K": 0.24},
		},
		"gemini-3.1-flash-image": {
			base:   0.067,
			prices: map[string]float64{"0.5K": 0.045, "1K": 0.067, "2K": 0.101, "4K": 0.151},
		},
	}
	for model, test := range geminiTests {
		base, ok := GetImageModelBasePrice(model)
		require.True(t, ok, model)
		require.InDelta(t, test.base, base, 1e-9, model)
		for resolution, want := range test.prices {
			got, found := GetImageModelPrice(model, resolution)
			require.True(t, found, model+" "+resolution)
			require.InDelta(t, want, got, 1e-9, model+" "+resolution)
			require.InDelta(t, want/test.base, GetImageModelPriceRatio(model, resolution), 1e-9, model+" "+resolution)
		}
	}
}

func TestImage25PricesCanBeChangedIndependently(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	previous := common.OptionMap
	common.OptionMap = map[string]string{ImageModelPricingOption: DefaultImageModelPricingJSON()}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previous
		common.OptionMapRWMutex.Unlock()
	})
	var configs map[string]imageModelPricing
	require.NoError(t, common.Unmarshal([]byte(DefaultImageModelPricingJSON()), &configs))
	for _, name := range []string{"gpt-image-2.5-sunburst", "gpt-image-2.5-flare"} {
		for tier, expected := range map[string]float64{"1K": 0.25, "2K": 0.3, "4K": 0.6} {
			price, ok := GetImageModelPrice(name, tier)
			require.True(t, ok)
			require.InDelta(t, expected, price, 1e-9)
		}
	}
	configs["gpt-image-2.5-sunburst"].Prices["4K"] = 0.9
	raw, err := common.Marshal(configs)
	require.NoError(t, err)
	common.OptionMapRWMutex.Lock()
	common.OptionMap[ImageModelPricingOption] = string(raw)
	common.OptionMapRWMutex.Unlock()
	for name, expected := range map[string]float64{"gpt-image-2": 0.6, "gpt-image-2.5-flare": 0.6, "gpt-image-2.5-sunburst": 0.9} {
		price, ok := GetImageModelPrice(name, "4K")
		require.True(t, ok)
		require.InDelta(t, expected, price, 1e-9, name)
	}
}
