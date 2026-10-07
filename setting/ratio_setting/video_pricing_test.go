package ratio_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/require"
)

func TestSeedancePricingRenamePreservesConfiguredPrices(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	previous, existed := common.OptionMap[VideoModelPricingOption]
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if existed {
			common.OptionMap[VideoModelPricingOption] = previous
		} else {
			delete(common.OptionMap, VideoModelPricingOption)
		}
	})
	for _, configuredName := range []string{"seedance-2.0", "doubao-seedance-2.0"} {
		common.OptionMapRWMutex.Lock()
		if common.OptionMap == nil {
			common.OptionMap = map[string]string{}
		}
		common.OptionMap[VideoModelPricingOption] = `{"` + configuredName + `":{"base_price":0.2,"prices":{"720P":0.2,"4K-input":0.6}}}`
		common.OptionMapRWMutex.Unlock()
		for _, requestedName := range []string{"seedance-2.0", "doubao-seedance-2.0"} {
			base, ok := GetVideoModelBasePrice(requestedName)
			require.True(t, ok)
			require.InDelta(t, 0.2, base, 1e-9)
			require.InDelta(t, 3.0, GetVideoModelResolutionRatio(requestedName, "4K-input"), 1e-9)
			price, ok := GetVideoModelPrice(requestedName, "480P")
			require.True(t, ok)
			require.InDelta(t, 0.07031, price, 1e-9)
		}
	}
}

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

func TestOfficialVideoPricingDoesNotFallbackToRegularPrices(t *testing.T) {
	price, ok := GetVideoModelOfficialBasePrice("kling-v3-omni")
	require.False(t, ok)
	require.Zero(t, price)
	require.Zero(t, GetVideoModelOfficialPriceRatio("kling-v3-omni", "base"))

	price, ok = GetVideoModelOfficialBasePrice("seedance-2.0")
	require.True(t, ok)
	require.InDelta(t, 0.1512, price, 1e-9)
	price, ok = GetVideoModelOfficialPrice("seedance-2.0", "1080P")
	require.True(t, ok)
	require.InDelta(t, 0.37422, price, 1e-9)
	_, ok = GetVideoModelOfficialPrice("seedance-2.0", "8K")
	require.False(t, ok)
}

func TestDefaultVideoModelPricingIncludesSeedanceResolutionPrices(t *testing.T) {
	base, ok := GetVideoModelBasePrice("doubao-seedance-2.0")
	require.True(t, ok)
	require.InDelta(t, 0.15120, base, 1e-9)

	tests := map[string]float64{
		"480P":        0.07031,
		"480P-input":  0.04319,
		"720P":        0.15120,
		"720P-input":  0.09288,
		"1080P":       0.37422,
		"1080P-input": 0.22842,
		"4K":          0.77760,
		"4K-input":    0.46656,
	}
	for resolution, want := range tests {
		got, found := GetVideoModelPrice("doubao-seedance-2.0", resolution)
		require.True(t, found, resolution)
		require.InDelta(t, want, got, 1e-9, resolution)
		require.InDelta(t, want/base, GetVideoModelResolutionRatio("doubao-seedance-2.0", resolution), 1e-9, resolution)
	}

	details, found := GetVideoModelPricingDetails("doubao-seedance-2.0")
	require.True(t, found)
	require.Equal(t, "second", details.Unit)
	require.Equal(t, "720P", details.BaseVariant)
	official := tests
	for variant, want := range official {
		require.InDelta(t, want, details.OfficialPrices[variant], 1e-9, variant)
	}
}

func TestDefaultVideoModelPricingIncludesSeedance25(t *testing.T) {
	base, ok := GetVideoModelBasePrice("seedance-2.5")
	require.True(t, ok)
	require.InDelta(t, 0.23112, base, 1e-9)
	for variant, want := range map[string]float64{"480P": 0.10280, "480P-input": 0.06149, "720P": 0.23112, "720P-input": 0.13824, "1080P": 0.56862, "1080P-input": 0.34020} {
		got, found := GetVideoModelPrice("seedance-2.5", variant)
		require.True(t, found, variant)
		require.InDelta(t, want, got, 1e-9, variant)
		require.InDelta(t, want/base, GetVideoModelResolutionRatio("seedance-2.5", variant), 1e-9, variant)
	}
}

func TestSeedanceFastMiniDefaultTariffsMatchOfficialPrices(t *testing.T) {
	for name, prices := range map[string]map[string]float64{
		"seedance-2.0-fast": {"480P": .05625, "480P-input": .03315, "720P": .12096, "720P-input": .07128},
		"seedance-2.0-mini": {"480P": .03515, "480P-input": .02109, "720P": .07560, "720P-input": .04536},
	} {
		details, ok := GetVideoModelPricingDetails(name)
		require.True(t, ok)
		require.Equal(t, "720P", details.BaseVariant)
		require.InDelta(t, prices["720P"], details.BasePrice, 1e-9)
		for variant, price := range prices {
			require.InDelta(t, price, details.Prices[variant], 1e-9)
			require.InDelta(t, price, details.OfficialPrices[variant], 1e-9)
		}
	}
}
