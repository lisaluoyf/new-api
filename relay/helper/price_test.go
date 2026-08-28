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
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
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

func TestForceFreeModelPriceDataClearsAllUserCharges(t *testing.T) {
	price := forceFreeModelPriceData(types.PriceData{
		ModelPrice: 2, ModelRatio: 3, CompletionRatio: 4, CacheRatio: 5,
		Quota: 100, QuotaToPreConsume: 200,
	})
	require.True(t, price.FreeModel)
	require.Zero(t, price.ModelPrice)
	require.Zero(t, price.ModelRatio)
	require.Zero(t, price.CompletionRatio)
	require.Zero(t, price.CacheRatio)
	require.Zero(t, price.Quota)
	require.Zero(t, price.QuotaToPreConsume)
}

func TestModelPriceHelperPerCallUsesExplicitMediaBasePrice(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ratio_setting.InitRatioSettings()

	common.OptionMapRWMutex.Lock()
	mapWasNil := common.OptionMap == nil
	if mapWasNil {
		common.OptionMap = make(map[string]string)
	}
	previous, hadPrevious := common.OptionMap[ratio_setting.VideoModelPricingOption]
	common.OptionMap[ratio_setting.VideoModelPricingOption] = ratio_setting.DefaultVideoModelPricingJSON()
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if mapWasNil {
			common.OptionMap = nil
			return
		}
		if hadPrevious {
			common.OptionMap[ratio_setting.VideoModelPricingOption] = previous
		} else {
			delete(common.OptionMap, ratio_setting.VideoModelPricingOption)
		}
	})

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("group", "default")
	info := &relaycommon.RelayInfo{
		OriginModelName: "doubao-seedance-2.0",
		UserGroup:       "default",
		UsingGroup:      "default",
	}

	price, err := ModelPriceHelperPerCall(ctx, info)
	require.NoError(t, err)
	require.InDelta(t, 0.142, price.ModelPrice, 1e-9)
	require.Equal(t, int(0.142*common.QuotaPerUnit*price.GroupRatioInfo.GroupRatio), price.Quota)
}

func TestImagePriceUsesResolutionBaseAndAllChannelCoefficients(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ratio_setting.InitRatioSettings()

	common.OptionMapRWMutex.Lock()
	mapWasNil := common.OptionMap == nil
	if mapWasNil {
		common.OptionMap = make(map[string]string)
	}
	previous, hadPrevious := common.OptionMap[ratio_setting.ImageModelPricingOption]
	common.OptionMap[ratio_setting.ImageModelPricingOption] = ratio_setting.DefaultImageModelPricingJSON()
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if mapWasNil {
			common.OptionMap = nil
			return
		}
		if hadPrevious {
			common.OptionMap[ratio_setting.ImageModelPricingOption] = previous
		} else {
			delete(common.OptionMap, ratio_setting.ImageModelPricingOption)
		}
	})

	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.Exec(`CREATE TABLE channels (
		id integer primary key, recharge_rate real, model_mapping text, setting text,
		apimaster_price_ratio real, model_price_ratios text
	)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO channels
		(id, recharge_rate, apimaster_price_ratio) VALUES (1, 0.5, 2)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE channel_model_pricings (
		id integer primary key, channel_id integer not null, model_name text not null,
		input_price real, output_price real, cache_price real, cache_creation_price real,
		group_ratio real, pricing_source text
	)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO channel_model_pricings
		(channel_id, model_name, input_price, group_ratio, pricing_source)
		VALUES (1, 'gpt-image-2', 0.002, 0.2, 'api')`).Error)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("group", "default")
	ctx.Set("channel_id", 1)
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-image-2",
		RelayMode:       relayconstant.RelayModeImagesGenerations,
		UserGroup:       "default",
		UsingGroup:      "default",
	}

	tests := map[string]float64{"1K": 0.25, "2K": 0.30, "4K": 0.60}
	for resolution, basePrice := range tests {
		price, priceErr := ModelPriceHelper(ctx, info, 0, &types.TokenCountMeta{ImagePriceVariant: resolution})
		require.NoError(t, priceErr, resolution)
		// channel group 0.2 × recharge 0.5 × APIMaster 2.0 = 0.2.
		want := basePrice * 0.2
		require.InDelta(t, want, price.ModelPrice, 1e-9, resolution)
		require.Equal(t, int(want*common.QuotaPerUnit*price.GroupRatioInfo.GroupRatio), price.QuotaToPreConsume, resolution)
	}
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

func TestBuildCodingPlanPriceDataUsesAllOfficialAxesAndLiveMultiplier(t *testing.T) {
	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.SubscriptionPlan{}, &model.UserSubscription{}))

	now := model.GetDBTimestamp()
	plan := model.SubscriptionPlan{
		Id: 15001, Title: "Coding Pro", PlanType: model.SubscriptionPlanTypeCodingPlan,
		PriceAmount: 39, Currency: "USD", DurationUnit: model.SubscriptionDurationDay,
		DurationValue: 30, Enabled: true, TierLevel: 1,
		CodingOfficialAmountUSD: 79, TotalAmount: int64(79 * common.QuotaPerUnit),
		CodingModelMultipliers: `{"deepseek-v4-pro":0.400}`,
	}
	require.NoError(t, db.Create(&plan).Error)
	require.NoError(t, db.Create(&model.UserSubscription{
		Id: 15002, UserId: 15003, PlanId: plan.Id, Status: "active",
		StartTime: now, EndTime: now + 30*86400, AmountTotal: plan.TotalAmount,
	}).Error)
	model.InvalidateSubscriptionPlanCache(plan.Id)
	t.Cleanup(func() { model.InvalidateSubscriptionPlanCache(plan.Id) })

	start := time.Date(2026, time.August, 17, 13, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	info := &relaycommon.RelayInfo{
		UserId: 15003, OriginModelName: "deepseek-v4-pro", StartTime: start,
	}
	price, err := BuildCodingPlanPriceData(nil, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	require.InDelta(t, 0.4, info.CodingPlanMultiplier, 0.0000001)
	require.InDelta(t, 0.66, info.CodingOfficialInputPrice, 0.0000001)
	require.InDelta(t, 1.98, info.CodingOfficialOutputPrice, 0.0000001)
	require.InDelta(t, 0.022, info.CodingOfficialCacheReadPrice, 0.0000001)
	require.InDelta(t, 0.66, info.CodingOfficialCacheWritePrice, 0.0000001)
	require.InDelta(t, 0.66/2*0.4, price.ModelRatio, 0.0000001)
	require.InDelta(t, 1.98/0.66, price.CompletionRatio, 0.0000001)
	require.InDelta(t, 0.022/0.66, price.CacheRatio, 0.0000001)
	require.InDelta(t, 1, price.CacheCreationRatio, 0.0000001)
}

func TestRefreshModelPriceForRetryKeepsCodingPlanPricingForTieredModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ratio_setting.InitRatioSettings()

	savedModelRatio := ratio_setting.ModelRatio2JSONString()
	savedCompletionRatio := ratio_setting.CompletionRatio2JSONString()
	savedCacheRatio := ratio_setting.CacheRatio2JSONString()
	savedCreateCacheRatio := ratio_setting.CreateCacheRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(savedModelRatio))
		require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(savedCompletionRatio))
		require.NoError(t, ratio_setting.UpdateCacheRatioByJSONString(savedCacheRatio))
		require.NoError(t, ratio_setting.UpdateCreateCacheRatioByJSONString(savedCreateCacheRatio))
	})
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"coding-tiered-test":2.5}`))
	require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(`{"coding-tiered-test":6}`))
	require.NoError(t, ratio_setting.UpdateCacheRatioByJSONString(`{"coding-tiered-test":0.1}`))
	require.NoError(t, ratio_setting.UpdateCreateCacheRatioByJSONString(`{"coding-tiered-test":1}`))

	savedBillingConfig := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		savedBillingConfig[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(savedBillingConfig))
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": `{"coding-tiered-test":"tiered_expr"}`,
		"billing_setting.billing_expr": `{"coding-tiered-test":"tier(\"standard\", p * 5 + c * 30 + cr * 0.5 + cw * 5)"}`,
	}))

	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.SubscriptionPlan{}, &model.UserSubscription{}))

	now := time.Now().Unix()
	plan := model.SubscriptionPlan{
		Id: 15101, Title: "Coding Tiered", PlanType: model.SubscriptionPlanTypeCodingPlan,
		PriceAmount: 39, Currency: "USD", DurationUnit: model.SubscriptionDurationDay,
		DurationValue: 30, Enabled: true, TierLevel: 1,
		CodingOfficialAmountUSD: 79, TotalAmount: int64(79 * common.QuotaPerUnit),
		CodingModelMultipliers: `{"coding-tiered-test":0.030}`,
	}
	require.NoError(t, db.Create(&plan).Error)
	require.NoError(t, db.Create(&model.UserSubscription{
		Id: 15102, UserId: 15103, PlanId: plan.Id, Status: "active",
		StartTime: now, EndTime: now + 30*86400, AmountTotal: plan.TotalAmount,
	}).Error)
	model.InvalidateSubscriptionPlanCache(plan.Id)
	t.Cleanup(func() { model.InvalidateSubscriptionPlanCache(plan.Id) })

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("group", "default")
	ctx.Set("channel_id", 164)
	info := &relaycommon.RelayInfo{
		UserId: 15103, OriginModelName: "coding-tiered-test",
		UserGroup: "default", UsingGroup: "default", StartTime: time.Now(),
	}

	initial, err := BuildCodingPlanPriceData(ctx, info, 26_606, &types.TokenCountMeta{})
	require.NoError(t, err)
	info.SetCodingPriceData(initial)
	require.True(t, info.ActivateCodingPriceData())

	ctx.Set("channel_id", 82)
	refreshed, err := RefreshModelPriceForRetry(ctx, info, 26_606, &types.TokenCountMeta{})
	require.NoError(t, err)
	require.Equal(t, model.SubscriptionPlanTypeCodingPlan, info.PriceDataSource)
	require.Nil(t, info.TieredBillingSnapshot)
	require.InDelta(t, 0.15/2, refreshed.ModelRatio, 0.0000001)
	require.InDelta(t, 6, refreshed.CompletionRatio, 0.0000001)
	require.InDelta(t, 0.1, refreshed.CacheRatio, 0.0000001)
	require.InDelta(t, refreshed.ModelRatio, info.PriceData.ModelRatio, 0.0000001)
	require.Greater(t, refreshed.QuotaToPreConsume, 0)
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

func TestTieredWalletUsesSelectedChannelUserPriceWhileTrialUsesOfficialPrice(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ratio_setting.InitRatioSettings()
	savedModelRatio := ratio_setting.ModelRatio2JSONString()
	savedCompletionRatio := ratio_setting.CompletionRatio2JSONString()
	savedCacheRatio := ratio_setting.CacheRatio2JSONString()
	savedCreateCacheRatio := ratio_setting.CreateCacheRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(savedModelRatio))
		require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(savedCompletionRatio))
		require.NoError(t, ratio_setting.UpdateCacheRatioByJSONString(savedCacheRatio))
		require.NoError(t, ratio_setting.UpdateCreateCacheRatioByJSONString(savedCreateCacheRatio))
	})
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"gpt-5.4":1.25}`))
	require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(`{"gpt-5.4":6}`))
	require.NoError(t, ratio_setting.UpdateCacheRatioByJSONString(`{"gpt-5.4":0.1}`))
	require.NoError(t, ratio_setting.UpdateCreateCacheRatioByJSONString(`{"gpt-5.4":1.25}`))

	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": `{"gpt-5.4":"tiered_expr"}`,
		"billing_setting.billing_expr": `{"gpt-5.4":"len <= 272000 ? tier(\"standard\", p * 2.5 + c * 15 + cr * 0.25) : tier(\"long_context\", p * 5 + c * 22.5 + cr * 0.5)"}`,
	}))

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
		(id, recharge_rate, apimaster_price_ratio) VALUES
		(7, 0.5, 1.5),
		(8, 0.5, 1.5)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO channel_model_pricings
		(channel_id, model_name, input_price, output_price, cache_price, cache_creation_price, group_ratio, pricing_source)
		VALUES
		(7, 'gpt-5.4', 2, 10, 0.2, 2.5, 1, 'api'),
		(8, 'gpt-5.4', 4, 20, 0.4, 5, 1, 'api')`).Error)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("group", "default")
	ctx.Set("channel_id", 7)
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5.4",
		UserGroup:       "default",
		UsingGroup:      "default",
		StartTime:       time.Now(),
	}

	walletPrice, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	walletSnapshot := info.WalletTieredBillingSnapshot
	require.NotNil(t, walletSnapshot)
	require.Equal(t, 7, walletSnapshot.PricingChannelID)
	require.True(t, walletSnapshot.PriceScale.Enabled)
	require.InDelta(t, 0.6, walletSnapshot.PriceScale.Input, 0.000001)
	require.InDelta(t, 0.5, walletSnapshot.PriceScale.Output, 0.000001)
	require.InDelta(t, 0.6, walletSnapshot.PriceScale.CacheRead, 0.000001)
	walletEstimatedCost := 1000*1.5 + float64(walletSnapshot.EstimatedCompletionTokens)*7.5
	require.Equal(t, billingexpr.QuotaRound(walletEstimatedCost/1_000_000*common.QuotaPerUnit*walletSnapshot.GroupRatio), walletPrice.QuotaToPreConsume)

	ok, walletQuota, result := service.TryTieredSettle(info, billingexpr.TokenParams{
		P: 800, C: 100, Len: 1000, CR: 200,
	})
	require.True(t, ok)
	require.NotNil(t, result)
	walletCost := 800*1.5 + 100*7.5 + 200*0.15
	require.Equal(t, billingexpr.QuotaRound(walletCost/1_000_000*common.QuotaPerUnit*walletSnapshot.GroupRatio), walletQuota)

	ctx.Set("channel_id", 8)
	_, err = RefreshModelPriceForRetry(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	require.Equal(t, 8, info.WalletTieredBillingSnapshot.PricingChannelID)
	require.InDelta(t, 1.2, info.WalletTieredBillingSnapshot.PriceScale.Input, 0.000001)
	require.InDelta(t, 1.0, info.WalletTieredBillingSnapshot.PriceScale.Output, 0.000001)

	trialPrice, err := BuildGPTTrialPriceData(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	require.NotNil(t, info.TrialTieredBillingSnapshot)
	require.False(t, info.TrialTieredBillingSnapshot.PriceScale.Enabled)
	require.Zero(t, info.TrialTieredBillingSnapshot.PriceScale.Input)
	trialEstimatedCost := 1000*2.5 + float64(info.TrialTieredBillingSnapshot.EstimatedCompletionTokens)*15
	require.Equal(t, billingexpr.QuotaRound(trialEstimatedCost/1_000_000*common.QuotaPerUnit), trialPrice.QuotaToPreConsume)
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
