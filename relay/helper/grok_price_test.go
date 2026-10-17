package helper

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http/httptest"
	"testing"
)

func TestGrokOfficialPrechargeThenActualTier(t *testing.T) {
	savedGroups := ratio_setting.GroupRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"grok-tier-test":1}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(savedGroups)) })
	oldDB := model.DB
	model.DB = nil
	t.Cleanup(func() { model.DB = oldDB })
	for _, name := range []string{"grok-4.5", "grok-4.6", "grok-4.7"} {
		for _, prompt := range []int{199999, 200000, 200001} {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Set("group", "grok-tier-test")
			ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{OriginModelName: name, UserGroup: "grok-tier-test", UsingGroup: "grok-tier-test", BillingRequestInput: &billingexpr.RequestInput{Body: []byte(`{"model":"` + name + `"}`)}}
			price, err := ModelPriceHelper(ctx, info, prompt, &types.TokenCountMeta{MaxTokens: 1000})
			require.NoError(t, err)
			require.NotNil(t, info.TieredBillingSnapshot)
			factor, tier := 1.0, "short_context"
			if prompt >= 200000 {
				factor = 2
				tier = "long_context"
			}
			require.Equal(t, tier, info.TieredBillingSnapshot.EstimatedTier)
			require.Equal(t, billingexpr.QuotaRound((float64(prompt)*2+1000*6)*factor/1e6*common.QuotaPerUnit), price.QuotaToPreConsume)
			// A cached input remains part of context length at settlement.
			u := &dto.Usage{PromptTokens: prompt, CompletionTokens: 1000}
			u.PromptTokensDetails.CachedTokens = 150000
			params := service.BuildTieredTokenParams(u, true, billingexpr.UsedVars(info.TieredBillingSnapshot.ExprString))
			applied, quota, result := service.TryTieredSettle(info, params)
			require.True(t, applied)
			require.Equal(t, tier, result.MatchedTier)
			cache := .5
			if name == "grok-4.5" {
				cache = .3
			}
			require.Equal(t, billingexpr.QuotaRound((float64(prompt-150000)*2+150000*cache+1000*6)*factor/1e6*common.QuotaPerUnit), quota)
		}
	}
}

func TestGrokChannelFallbackKeepsOfficialTierAndRepricesOnce(t *testing.T) {
	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.ChannelModelPricing{}))
	savedGroups := ratio_setting.GroupRatio2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(savedGroups)) })
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"grok-tier-test":1}`))
	recharge, markup := .15, 2.0
	for _, id := range []int{1, 2} {
		require.NoError(t, db.Create(&model.Channel{Id: id, RechargeRate: &recharge, ApimasterPriceRatio: &markup}).Error)
		scale := float64(id)
		require.NoError(t, db.Create(&model.ChannelModelPricing{ChannelId: id, ModelName: "grok-4.6", InputPrice: .2 * scale, OutputPrice: .6 * scale, CachePrice: .05 * scale, GroupRatio: .1 * scale}).Error)
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("channel_id", 1)
	ctx.Set("group", "grok-tier-test")
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{OriginModelName: "grok-4.6", UserGroup: "grok-tier-test", UsingGroup: "grok-tier-test", BillingRequestInput: &billingexpr.RequestInput{Body: []byte(`{}`)}}
	price, err := ModelPriceHelper(ctx, info, 200000, &types.TokenCountMeta{MaxTokens: 1000})
	require.NoError(t, err)
	require.Equal(t, "long_context", info.TieredBillingSnapshot.EstimatedTier)
	first := price.QuotaToPreConsume
	for _, id := range []int{2, 2} {
		ctx.Set("channel_id", id)
		refreshed, err := RefreshModelPriceForRetry(ctx, info, 200000, &types.TokenCountMeta{MaxTokens: 1000})
		require.NoError(t, err)
		require.Equal(t, first*2, refreshed.QuotaToPreConsume)
		require.Equal(t, 2, info.TieredBillingSnapshot.PricingChannelID)
		usage := &dto.Usage{PromptTokens: 200000, CompletionTokens: 1000}
		usage.PromptTokensDetails.CachedTokens = 150000
		_, quota, result := service.TryTieredSettle(info, service.BuildTieredTokenParams(usage, true, billingexpr.UsedVars(info.TieredBillingSnapshot.ExprString)))
		require.Equal(t, "long_context", result.MatchedTier)
		require.Equal(t, 10860, quota)
	}
}
