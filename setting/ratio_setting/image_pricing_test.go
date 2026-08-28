package ratio_setting

import (
	"testing"

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
