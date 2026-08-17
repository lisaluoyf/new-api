package helper

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDeepSeekV4PriceUsesOfficialScheduleAndUserMultiplier(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ratio_setting.InitRatioSettings()

	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.Exec(`CREATE TABLE channels (
		id integer primary key, recharge_rate real, model_mapping text, setting text,
		apimaster_price_ratio real, model_price_ratios text
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE channel_model_pricings (
		id integer primary key, channel_id integer not null, model_name text not null,
		input_price real, output_price real, cache_price real, cache_creation_price real,
		group_ratio real, pricing_source text
	)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO channels
		(id, recharge_rate, apimaster_price_ratio, model_price_ratios)
		VALUES (1, 0.1, 1.5, '{"deepseek-v4-flash":0.8}')`).Error)
	// Stored unit prices are ignored, but the upstream group multiplier remains part of billing.
	require.NoError(t, db.Exec(`INSERT INTO channel_model_pricings
		(channel_id, model_name, input_price, output_price, cache_price, cache_creation_price, group_ratio, pricing_source)
		VALUES (1, 'deepseek-v4-flash', 9, 27, 0.9, 9, 0.5, 'api')`).Error)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("group", "default")
	ctx.Set("channel_id", 1)
	info := &relaycommon.RelayInfo{
		OriginModelName: "deepseek-v4-flash",
		UserGroup:       "default",
		UsingGroup:      "default",
		StartTime:       time.Date(2026, time.August, 17, 13, 0, 0, 0, time.FixedZone("CST", 8*60*60)),
	}

	price, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	require.InDelta(t, 0.0088/service.PlatformUSDPerModelRatio, price.ModelRatio, 0.0000001)
	require.InDelta(t, 3, price.CompletionRatio, 0.0000001)
	require.InDelta(t, 0.007/0.22, price.CacheRatio, 0.0000001)
	require.InDelta(t, 1, price.CacheCreationRatio, 0.0000001)
}

func TestDeepSeekV4RetryKeepsRequestTimeButRefreshesChannelMultiplier(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ratio_setting.InitRatioSettings()

	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.Exec(`CREATE TABLE channels (
		id integer primary key, recharge_rate real, model_mapping text, setting text,
		apimaster_price_ratio real, model_price_ratios text
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE channel_model_pricings (
		id integer primary key, channel_id integer not null, model_name text not null,
		input_price real, output_price real, cache_price real, cache_creation_price real,
		group_ratio real, pricing_source text
	)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO channels (id, recharge_rate, apimaster_price_ratio) VALUES
		(1, 0.5, 1), (2, 0.25, 1.5)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO channel_model_pricings
		(channel_id, model_name, input_price, output_price, group_ratio, pricing_source) VALUES
		(1, 'deepseek-v4-pro', 9, 27, 0.8, 'api'),
		(2, 'deepseek-v4-pro', 9, 27, 0.4, 'api')`).Error)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("group", "default")
	ctx.Set("channel_id", 1)
	info := &relaycommon.RelayInfo{
		OriginModelName: "deepseek-v4-pro",
		UserGroup:       "default",
		UsingGroup:      "default",
		StartTime:       time.Date(2026, time.August, 17, 9, 0, 0, 0, time.FixedZone("CST", 8*60*60)),
	}

	initial, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	require.InDelta(t, 1.32*0.8*0.5/service.PlatformUSDPerModelRatio, initial.ModelRatio, 0.0000001)

	ctx.Set("channel_id", 2)
	refreshed, err := RefreshModelPriceForRetry(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	require.InDelta(t, 1.32*0.4*0.25*1.5/service.PlatformUSDPerModelRatio, refreshed.ModelRatio, 0.0000001)
}

func TestRefreshModelPriceForRetryUsesFallbackChannelPricing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ratio_setting.InitRatioSettings()

	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db

	require.NoError(t, db.Exec(`CREATE TABLE channels (
		id integer primary key, recharge_rate real, model_mapping text, setting text,
		apimaster_price_ratio real, model_price_ratios text
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE channel_model_pricings (
		id integer primary key, channel_id integer not null, model_name text not null,
		input_price real, output_price real, cache_price real, cache_creation_price real,
		group_ratio real, pricing_source text
	)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO channels (id, recharge_rate, model_mapping) VALUES
		(1, 1, ''), (2, 1, '')`).Error)
	require.NoError(t, db.Exec(`INSERT INTO channel_model_pricings
		(channel_id, model_name, input_price, output_price, cache_price, cache_creation_price, group_ratio, pricing_source)
		VALUES
		(1, 'fallback-price-test-model', 2, 4, 0.2, 2.5, 1, 'api'),
		(2, 'fallback-price-test-model', 6, 24, 0.3, 7.5, 1, 'api')`).Error)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("group", "default")
	ctx.Set("channel_id", 1)
	info := &relaycommon.RelayInfo{
		OriginModelName: "fallback-price-test-model",
		UserGroup:       "default",
		UsingGroup:      "default",
	}

	initial, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	require.InDelta(t, 1.0, initial.ModelRatio, 0.000001)
	require.InDelta(t, 2.0, initial.CompletionRatio, 0.000001)

	ctx.Set("channel_id", 2)
	refreshed, err := RefreshModelPriceForRetry(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	require.InDelta(t, 3.0, refreshed.ModelRatio, 0.000001)
	require.InDelta(t, 4.0, refreshed.CompletionRatio, 0.000001)
	require.InDelta(t, refreshed.ModelRatio, info.PriceData.ModelRatio, 0.000001)
}

func TestBuildGPTTrialPriceDataIgnoresChannelPricing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ratio_setting.InitRatioSettings()

	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db

	require.NoError(t, db.Exec(`CREATE TABLE channels (
		id integer primary key, recharge_rate real, model_mapping text, setting text,
		apimaster_price_ratio real, model_price_ratios text
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE channel_model_pricings (
		id integer primary key, channel_id integer not null, model_name text not null,
		input_price real, output_price real, cache_price real, cache_creation_price real,
		group_ratio real, pricing_source text
	)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO channels (id, recharge_rate, model_mapping) VALUES (1, 1, '')`).Error)
	require.NoError(t, db.Exec(`INSERT INTO channel_model_pricings
		(channel_id, model_name, input_price, output_price, cache_price, cache_creation_price, group_ratio, pricing_source)
		VALUES
		(1, 'gpt-5', 8, 32, 0.3, 7.5, 1, 'api')`).Error)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("group", "default")
	ctx.Set("channel_id", 1)
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5",
		UserGroup:       "default",
		UsingGroup:      "default",
	}

	walletPrice, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)

	trialPrice, err := BuildGPTTrialPriceData(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)

	expectedTrialRatio, ok, _ := ratio_setting.GetModelRatio("gpt-5")
	require.True(t, ok)
	require.NotEqual(t, walletPrice.ModelRatio, trialPrice.ModelRatio)
	require.InDelta(t, expectedTrialRatio, trialPrice.ModelRatio, 0.000001)
	require.InDelta(t, 1.0, trialPrice.GroupRatioInfo.GroupRatio, 0.000001)
	require.NotNil(t, info.TrialPriceData)
}

func TestModelPriceHelperTieredUsesPreloadedRequestInput(t *testing.T) {
	gin.SetMode(gin.TestMode)

	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})

	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": `{"tiered-test-model":"tiered_expr"}`,
		"billing_setting.billing_expr": `{"tiered-test-model":"param(\"stream\") == true ? tier(\"stream\", p * 3) : tier(\"base\", p * 2)"}`,
	}))

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/channel/test/1", nil)
	req.Body = nil
	req.ContentLength = 0
	req.Header.Set("Content-Type", "application/json")
	ctx.Request = req
	ctx.Set("group", "default")

	info := &relaycommon.RelayInfo{
		OriginModelName: "tiered-test-model",
		UserGroup:       "default",
		UsingGroup:      "default",
		RequestHeaders:  map[string]string{"Content-Type": "application/json"},
		BillingRequestInput: &billingexpr.RequestInput{
			Headers: map[string]string{"Content-Type": "application/json"},
			Body:    []byte(`{"stream":true}`),
		},
	}

	priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	require.Equal(t, billingexpr.QuotaRound(1500*ratio_setting.GetGroupRatio("default")), priceData.QuotaToPreConsume)
	require.NotNil(t, info.TieredBillingSnapshot)
	require.Equal(t, "stream", info.TieredBillingSnapshot.EstimatedTier)
	require.Equal(t, billing_setting.BillingModeTieredExpr, info.TieredBillingSnapshot.BillingMode)
	require.Equal(t, common.QuotaPerUnit, info.TieredBillingSnapshot.QuotaPerUnit)

	snapshot := info.TieredBillingSnapshot
	refreshed, err := RefreshModelPriceForRetry(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	require.Same(t, snapshot, info.TieredBillingSnapshot)
	require.Equal(t, priceData, refreshed)
}

func TestTieredPriceSnapshotFollowsSelectedFundingSource(t *testing.T) {
	walletPrice := types.PriceData{
		GroupRatioInfo:    types.GroupRatioInfo{GroupRatio: 0.42},
		QuotaToPreConsume: 420,
	}
	trialPrice := types.PriceData{
		GroupRatioInfo:    types.GroupRatioInfo{GroupRatio: 1},
		QuotaToPreConsume: 1000,
	}
	walletSnapshot := &billingexpr.BillingSnapshot{GroupRatio: 0.42}
	trialSnapshot := &billingexpr.BillingSnapshot{GroupRatio: 1}

	info := &relaycommon.RelayInfo{TieredBillingSnapshot: walletSnapshot}
	info.SetWalletPriceData(walletPrice)
	info.TieredBillingSnapshot = trialSnapshot
	info.SetTrialPriceData(trialPrice)
	require.Equal(t, "wallet", info.PriceDataSource)
	require.Same(t, walletSnapshot, info.TieredBillingSnapshot)

	require.True(t, info.ActivateGPTPromotionalPriceData(model.SubscriptionPlanTypeGPTReferralReward))
	require.Equal(t, model.SubscriptionPlanTypeGPTReferralReward, info.PriceDataSource)
	require.Equal(t, trialPrice, info.PriceData)
	require.Same(t, trialSnapshot, info.TieredBillingSnapshot)
	require.InDelta(t, 1, info.TieredBillingSnapshot.GroupRatio, 0.000001)

	require.True(t, info.ActivateWalletPriceData())
	require.Equal(t, "wallet", info.PriceDataSource)
	require.Equal(t, walletPrice, info.PriceData)
	require.Same(t, walletSnapshot, info.TieredBillingSnapshot)
	require.InDelta(t, 0.42, info.TieredBillingSnapshot.GroupRatio, 0.000001)

	require.True(t, info.ActivateTrialPriceData())
	require.Equal(t, "gpt_trial", info.PriceDataSource)
	require.Equal(t, trialPrice, info.PriceData)
	require.Same(t, trialSnapshot, info.TieredBillingSnapshot)
}
