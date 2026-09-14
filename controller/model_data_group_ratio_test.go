package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

func TestManualPricingFallbackReplacesAPIPriceInDisplay(t *testing.T) {
	for _, config := range []struct {
		previous map[string]float64
		update   func(string) error
		value    string
	}{
		{ratio_setting.GetModelRatioCopy(), ratio_setting.UpdateModelRatioByJSONString, `{"kimi-k3":1.5}`},
		{ratio_setting.GetCompletionRatioCopy(), ratio_setting.UpdateCompletionRatioByJSONString, `{"kimi-k3":5}`},
	} {
		previous, err := common.Marshal(config.previous)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, config.update(string(previous))) })
		require.NoError(t, config.update(config.value))
	}
	db := setupModelDataToggleTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.ChannelModelPricing{}))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(upstream.Close)
	setting, baseURL := `{"key_group":"Kimi","manual_group_ratio":0.65}`, upstream.URL
	channel := model.Channel{Id: 91, Setting: &setting, BaseURL: &baseURL}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, db.Create(&model.ChannelModelPricing{
		ChannelId: 91, ModelName: "kimi-k3", GroupRatio: 0.8,
		InputPrice: 16, OutputPrice: 80, PricingSource: "api",
	}).Error)
	service.FetchChannelPricing(&channel)
	_, hasStored := service.LookupPreferredChannelPricingRow(91, "kimi-k3", nil)
	require.False(t, hasStored)
	var in, out, cache, write, group *float64
	var source *string
	applyPublicManualPricingToRow(&setting, "kimi-k3", &in, &out, &cache, &write, &group, &source)
	applyModelGroupRatioToRow(&setting, "kimi-k3", &in, &out, &cache, &write, &group)
	require.Equal(t, "manual", *source)
	require.InDelta(t, 1.95, *in, 1e-9)
	require.InDelta(t, 9.75, *out, 1e-9)
	require.Equal(t, 0.65, *group)
	require.InDelta(t, 3, *in / *group, 1e-9, "channel base price must use official pricing")
	recharge := 0.148907
	item := publicMarketplacePriceItem("kimi-k3", publicMarketplacePricingRow{
		Setting: &setting, InputPrice: in, OutputPrice: out, GroupRatio: group,
		RechargeRate: &recharge, ApimasterPriceRatio: 1.5,
	})
	require.InDelta(t, 1.95*recharge*1.5, *item.UserPrice, 1e-9)
	require.InDelta(t, 9.75*recharge*1.5, *item.ActualOutputUserPrice, 1e-9)
}

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

func TestManualGroupRatioDisplayWithStalePricing(t *testing.T) {
	setting := `{"manual_group_ratio":0.65}`
	in, out, cache, write, group, recharge := 16.0, 80.0, 1.6, 3.2, 0.8, 0.148907
	ip, op, cp, wp, gp := &in, &out, &cache, &write, &group
	applyModelGroupRatioToRow(&setting, "kimi-k3", &ip, &op, &cp, &wp, &gp)
	require.InDelta(t, 13, *ip, 1e-9)
	require.InDelta(t, 65, *op, 1e-9)
	require.InDelta(t, 1.3, *cp, 1e-9)
	require.InDelta(t, 2.6, *wp, 1e-9)
	require.Equal(t, 0.65, *gp)
	require.Equal(t, 16.0, in, "shared snapshot must remain unchanged")
	applyModelGroupRatioToRow(&setting, "kimi-k3", &ip, &op, &cp, &wp, &gp)
	require.InDelta(t, 13, *ip, 1e-9, "do not apply the multiplier twice")
	item := publicMarketplacePriceItem("kimi-k3", publicMarketplacePricingRow{
		Setting: &setting, InputPrice: &in, OutputPrice: &out, GroupRatio: &group,
		RechargeRate: &recharge, ApimasterPriceRatio: 1,
	})
	require.InDelta(t, 13*recharge, *item.UserPrice, 1e-9)
	require.InDelta(t, 65*recharge, *item.ActualOutputUserPrice, 1e-9)
}
