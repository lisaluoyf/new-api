package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultVideoModelPricingIncludesKlingOmniTiers(t *testing.T) {
	tests := map[string]float64{
		"base":      0.084,
		"sound":     0.112,
		"video":     0.126,
		"pro":       0.112,
		"pro-sound": 0.14,
		"pro-video": 0.168,
		"4k":        0.5357,
		"4k-sound":  0.5357,
	}
	for variant, want := range tests {
		got, ok := GetVideoModelPrice("kling-v3-omni", variant)
		require.True(t, ok, variant)
		require.InDelta(t, want, got, 1e-9, variant)
		require.InDelta(t, want/0.084, GetVideoModelPriceRatio("kling-v3-omni", variant), 1e-9, variant)
	}
}

func TestDefaultVideoModelPricingIncludesSeedanceResolutionPrices(t *testing.T) {
	base, ok := GetVideoModelBasePrice("doubao-seedance-2.0")
	require.True(t, ok)
	require.InDelta(t, 0.142, base, 1e-9)

	tests := map[string]float64{
		"480P":        0.066,
		"480P-input":  0.04,
		"720P":        0.142,
		"720P-input":  0.08584,
		"1080P":       0.3544,
		"1080P-input": 0.21568,
		"4K":          0.722,
		"4K-input":    0.44432,
	}
	for resolution, want := range tests {
		got, found := GetVideoModelPrice("doubao-seedance-2.0", resolution)
		require.True(t, found, resolution)
		require.InDelta(t, want, got, 1e-9, resolution)
		require.InDelta(t, want/base, GetVideoModelResolutionRatio("doubao-seedance-2.0", resolution), 1e-9, resolution)
	}
}
