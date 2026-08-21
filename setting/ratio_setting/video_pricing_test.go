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
