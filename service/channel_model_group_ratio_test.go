package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEffectiveManualGroupRatio(t *testing.T) {
	for _, test := range []struct {
		name, setting, model string
		want                 float64
	}{
		{"override", `{"manual_group_ratio":0.5,"model_group_ratios":{"a":0.2}}`, "a", 0.2},
		{"default", `{"manual_group_ratio":0.5,"model_group_ratios":{"a":0.2}}`, "b", 0.5},
		{"model only", `{"model_group_ratios":{"a":0.2}}`, "a", 0.2},
		{"upstream", `{"model_group_ratios":{"a":0.2}}`, "b", 0},
		{"invalid override", `{"manual_group_ratio":0.5,"model_group_ratios":{"a":-1}}`, "a", 0.5},
		{"exact name", `{"model_group_ratios":{"a":0.2}}`, "a-variant", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, EffectiveManualGroupRatio(&test.setting, test.model))
		})
	}
}

func TestModelGroupRatioMappingBillingAndRemoval(t *testing.T) {
	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.ChannelModelPricing{}))
	setting := `{"manual_group_ratio":0.5,"model_group_ratios":{"a":0.2}}`
	mapping := `{"a":"upstream","b":"upstream"}`
	markup := `{"a":2}`
	recharge, defaultMarkup := 0.8, 1.5
	channel := model.Channel{Id: 1, Setting: &setting, ModelMapping: &mapping,
		RechargeRate: &recharge, ApimasterPriceRatio: &defaultMarkup, ModelPriceRatios: &markup}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, db.Create(&model.ChannelModelPricing{
		ChannelId: 1, ModelName: "upstream", GroupRatio: 0.5, PricingSource: "api",
		InputPrice: 10, OutputPrice: 20, CachePrice: 1, CacheCreationPrice: 2,
	}).Error)

	for _, test := range []struct {
		model string
		want  float64
	}{{"a", 6.4}, {"b", 12}} {
		prices, ok, err := ChannelUserPricesResolvedForModel(1, test.model)
		require.NoError(t, err)
		require.True(t, ok)
		require.InDelta(t, test.want, prices.InputPrice, 1e-9)
		require.InDelta(t, test.want*2, prices.OutputPrice, 1e-9)
		require.InDelta(t, test.want/10, prices.CachePrice, 1e-9)
		require.InDelta(t, test.want/5, prices.CacheCreationPrice, 1e-9)
		ratios, ok := ChannelModelPriceData(1, test.model)
		require.True(t, ok)
		require.InDelta(t, test.want/2, ratios.ModelRatio, 1e-9)
		logged, err := ChannelActualPricesResolved(1, test.model)
		require.NoError(t, err)
		require.InDelta(t, test.want, logged.InputPrice, 1e-9)
	}

	// Removing an override must work immediately, without fetching upstream.
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 1).
		Update("setting", `{"manual_group_ratio":0.5}`).Error)
	prices, ok, err := ChannelUserPricesResolvedForModel(1, "a")
	require.NoError(t, err)
	require.True(t, ok)
	require.InDelta(t, 16, prices.InputPrice, 1e-9)
	stored, err := model.GetChannelModelPricing(1, "upstream")
	require.NoError(t, err)
	require.Equal(t, 10.0, stored.InputPrice)
	require.Equal(t, 0.5, stored.GroupRatio)

	// With no channel default, removal returns to the stored upstream ratio.
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 1).
		Update("setting", `{"model_group_ratios":{"a":1}}`).Error)
	prices, ok, err = ChannelUserPricesResolvedForModel(1, "a")
	require.NoError(t, err)
	require.True(t, ok)
	require.InDelta(t, 32, prices.InputPrice, 1e-9)
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 1).Update("setting", `{}`).Error)
	prices, ok, err = ChannelUserPricesResolvedForModel(1, "a")
	require.NoError(t, err)
	require.True(t, ok)
	require.InDelta(t, 16, prices.InputPrice, 1e-9)
}

func TestModelGroupRatioManualFallbackAndRouting(t *testing.T) {
	previous, err := common.Marshal(ratio_setting.GetModelRatioCopy())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(string(previous))) })
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"gpt-5.4":1.25}`))
	setting := `{"model_group_ratios":{"gpt-5.4":0.2}}`
	base, _, _, _, ok := GlobalModelPricingUSD("gpt-5.4")
	require.True(t, ok)
	manual, ok := LookupPublicManualPricing(&setting, "gpt-5.4")
	require.True(t, ok)
	require.InDelta(t, base*0.2, manual.InputPrice, 1e-9)
	price, ok := routeCandidateUserInputPrice(pricedRouteCandidate{
		Setting: &setting, HasInputPrice: true, InputPrice: 10, GroupRatio: 0.5,
		RechargeRate: 0.8, ApimasterPriceRatio: 2,
	}, "gpt-5.4", base)
	require.True(t, ok)
	require.InDelta(t, 6.4, price, 1e-9)

	timedSetting := `{"manual_group_ratio":0.5,"model_group_ratios":{"deepseek-v4-flash":0.2}}`
	at := time.Date(2026, 9, 8, 2, 0, 0, 0, time.UTC)
	timed, ok := DeepSeekV4OfficialPricingAt("deepseek-v4-flash", at)
	require.True(t, ok)
	price, ok = routeCandidateUserInputPriceAt(pricedRouteCandidate{
		Setting: &timedSetting, GroupRatio: 0.5, RechargeRate: 1, ApimasterPriceRatio: 1,
	}, "deepseek-v4-flash", 0, at)
	require.True(t, ok)
	require.InDelta(t, timed.InputPrice*0.2, price, 1e-9)
}
