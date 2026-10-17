package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestGrokChannelTierPricesAndFrozenAccounting(t *testing.T) {
	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.ChannelModelPricing{}))
	recharge, markup := .15, 2.0
	require.NoError(t, db.Create(&model.Channel{Id: 94, Name: "grok-tier-test", RechargeRate: &recharge, ApimasterPriceRatio: &markup}).Error)
	for _, name := range []string{"grok-4.5", "grok-4.6", "grok-4.7"} {
		cache := .5
		if name == "grok-4.5" {
			cache = .3
		}
		// Different token-class discounts catch accidental reliance on legacy cache
		// ratios or applying the recharge/markup multiplier more than once.
		row := model.ChannelModelPricing{ChannelId: 94, ModelName: name, InputPrice: .2, OutputPrice: 1.2, CachePrice: cache * .3, GroupRatio: .1}
		require.NoError(t, db.Create(&row).Error)
		expr, _ := billing_setting.GetBillingExpr(name)
		proc, err := ResolveTokenBillingExpression(94, name, true, time.Now())
		require.NoError(t, err)
		require.NotNil(t, proc)
		for _, prompt := range []int{199999, 200000, 200001} {
			u := &dto.Usage{PromptTokens: prompt, CompletionTokens: 1000}
			u.PromptTokensDetails.CachedTokens = 150000
			params := BuildTieredTokenParams(u, true, billingexpr.UsedVars(expr))
			result, err := billingexpr.ComputeTieredQuota(proc, params)
			require.NoError(t, err)
			factor, tier := 1.0, "short_context"
			if prompt >= 200000 {
				factor = 2
				tier = "long_context"
			}
			want := (float64(prompt-150000)*.2 + 1000*1.2 + 150000*cache*.3) * recharge * factor / 1e6
			require.InDelta(t, want, result.ActualQuotaBeforeGroup/common.QuotaPerUnit, 1e-12)
			require.Equal(t, tier, result.MatchedTier)
		}
		// In-flight snapshots must keep their original prices after catalog refresh.
		require.NoError(t, db.Model(&model.ChannelModelPricing{}).Where("id = ?", row.Id).Update("input_price", 99).Error)
		u := &dto.Usage{PromptTokens: 200000, CompletionTokens: 1000}
		u.PromptTokensDetails.CachedTokens = 150000
		r, err := billingexpr.ComputeTieredQuota(proc, BuildTieredTokenParams(u, true, billingexpr.UsedVars(expr)))
		require.NoError(t, err)
		require.InDelta(t, (50000*.2+1000*1.2+150000*cache*.3)*recharge*2/1e6, r.ActualQuotaBeforeGroup/common.QuotaPerUnit, 1e-12)
	}
}

// A supplier's flat schedule and old <= threshold must not disable the
// official customer tier. Procurement must retain the supplier's own schedule.
func TestGrokRetailOfficialTierOverridesSupplierShape(t *testing.T) {
	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.ChannelModelPricing{}))
	recharge, markup := .15, 2.0
	require.NoError(t, db.Create(&model.Channel{Id: 95, RechargeRate: &recharge, ApimasterPriceRatio: &markup}).Error)
	for _, expr := range []string{`tier("base",p*2+cr*.5+c*6)`, `len<=200000?tier("standard",p*2+cr*.5+c*6):tier("long_context",p*4+cr+c*12)`} {
		for _, name := range []string{"grok-4.5", "grok-4.6", "grok-4.7"} {
			row := model.ChannelModelPricing{ChannelId: 95, ModelName: name, InputPrice: .2, OutputPrice: .6, CachePrice: .05, GroupRatio: .1, BillingMode: "tiered_expr", BillingExpr: expr}
			require.NoError(t, db.Create(&row).Error)
			retail, err := ResolveTokenBillingExpression(95, name, false, time.Now())
			require.NoError(t, err)
			proc, err := ResolveTokenBillingExpression(95, name, true, time.Now())
			require.NoError(t, err)
			require.Equal(t, "global_expression", retail.Source)
			require.Equal(t, "channel_expression", proc.Source)
			usage := &dto.Usage{PromptTokens: 200000, CompletionTokens: 1000}
			usage.PromptTokensDetails.CachedTokens = 150000
			params := BuildTieredTokenParams(usage, true, billingexpr.UsedVars(retail.ExprString))
			r, err := billingexpr.ComputeTieredQuota(retail, params)
			require.NoError(t, err)
			require.Equal(t, "long_context", r.MatchedTier)
			require.InDelta(t, (50000*.2+150000*.05+1000*.6)*recharge*markup*2/1e6, r.ActualQuotaBeforeGroup/common.QuotaPerUnit, 1e-12)
			p, err := billingexpr.ComputeTieredQuota(proc, params)
			require.NoError(t, err)
			require.NotEqual(t, "long_context", p.MatchedTier)
			require.NoError(t, db.Delete(&row).Error)
		}
	}
}
