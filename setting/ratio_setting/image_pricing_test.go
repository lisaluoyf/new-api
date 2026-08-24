package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultImageModelPricingIncludesGPTImage2ResolutionPrices(t *testing.T) {
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
}
