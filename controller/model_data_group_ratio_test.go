package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModelGroupRatioDisplayMatchesBilling(t *testing.T) {
	setting := `{"manual_group_ratio":0.5,"model_group_ratios":{"priced-model":0.2}}`
	in, out, cache, write, group, recharge := 10.0, 20.0, 1.0, 2.0, 0.5, 0.8
	ip, op, cp, wp, gp := &in, &out, &cache, &write, &group
	applyModelGroupRatioToRow(&setting, "priced-model", &ip, &op, &cp, &wp, &gp)
	require.InDelta(t, 4, *ip, 1e-9)
	require.InDelta(t, 8, *op, 1e-9)
	require.InDelta(t, 0.4, *cp, 1e-9)
	require.InDelta(t, 0.8, *wp, 1e-9)
	require.Equal(t, 0.2, *gp)
	require.Equal(t, 10.0, in, "shared pricing snapshot must not be mutated")
	item := publicMarketplacePriceItem("priced-model", publicMarketplacePricingRow{
		Setting: &setting, InputPrice: &in, OutputPrice: &out, GroupRatio: &group,
		RechargeRate: &recharge, ApimasterPriceRatio: 2,
	})
	require.InDelta(t, 6.4, *item.UserPrice, 1e-9)
	require.InDelta(t, 12.8, *item.ActualOutputUserPrice, 1e-9)
	// A live manual fallback may already have the override. Do not multiply twice.
	applyModelGroupRatioToRow(&setting, "priced-model", &ip, &op, &cp, &wp, &gp)
	require.InDelta(t, 4, *ip, 1e-9)
}

func TestInvalidateMarketplacePriceCache(t *testing.T) {
	publicMarketplaceCache.Lock()
	publicMarketplaceCache.data["test-model"] = publicMarketplaceCacheEntry{}
	previous := publicMarketplaceCache.generation
	publicMarketplaceCache.Unlock()
	invalidatePublicMarketplaceCache()
	publicMarketplaceCache.Lock()
	defer publicMarketplaceCache.Unlock()
	require.Empty(t, publicMarketplaceCache.data)
	require.Greater(t, publicMarketplaceCache.generation, previous)
}
