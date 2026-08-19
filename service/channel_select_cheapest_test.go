package service

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAutoCheapestGroupName(t *testing.T) {
	if AutoCheapestGroup != "default" {
		t.Fatalf("AutoCheapestGroup = %q, want default", AutoCheapestGroup)
	}
}

func TestPricingRefreshDeletionPreservesFreeModelRows(t *testing.T) {
	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.ChannelModelPricing{}))
	require.NoError(t, db.Create(&[]model.ChannelModelPricing{
		{ChannelId: 9, ModelName: FreeModelID, InputPrice: 0.0001, PricingSource: "free_model"},
		{ChannelId: 9, ModelName: "provider/paid", InputPrice: 1, PricingSource: "api"},
	}).Error)
	require.NoError(t, deleteRefreshableChannelPricings(9))
	var rows []model.ChannelModelPricing
	require.NoError(t, db.Where("channel_id = ?", 9).Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Equal(t, FreeModelID, rows[0].ModelName)
}

func TestFreeModelAlwaysUsesAutoCheapest(t *testing.T) {
	if !usesAutoCheapest("paid-premium-group", FreeModelID) {
		t.Fatal("FreeModel must use Auto Cheapest independently of the token group")
	}
	if usesAutoCheapest("paid-premium-group", "gpt-5.4") {
		t.Fatal("ordinary models must preserve their existing group routing")
	}
}

func TestRouteCandidateUserInputPriceUsesManualPublicPricing(t *testing.T) {
	if err := ratio_setting.UpdateModelRatioByJSONString(`{"gpt-5.4":1.25}`); err != nil {
		t.Fatal(err)
	}
	setting := `{"manual_group_ratio":0.1,"model_price_ratio":0}`
	got, ok := routeCandidateUserInputPrice(pricedRouteCandidate{
		Setting:             &setting,
		RechargeRate:        0.146895,
		ApimasterPriceRatio: 3,
	}, "gpt-5.4", 2.5)
	if !ok {
		t.Fatal("expected price")
	}
	want := 0.11017125
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("price=%v want %v", got, want)
	}
}

func TestRouteCandidateInputPriceStoredRowWinsOverManualPublicPricing(t *testing.T) {
	if err := ratio_setting.UpdateModelRatioByJSONString(`{"gpt-5.4":1.25}`); err != nil {
		t.Fatal(err)
	}
	setting := `{"manual_group_ratio":0.1,"model_price_ratio":0}`
	got, ok := routeCandidateInputPrice(pricedRouteCandidate{
		Setting:       &setting,
		InputPrice:    0.75,
		HasInputPrice: true,
	}, "gpt-5.4", 2.5)
	if !ok {
		t.Fatal("expected price")
	}
	if got != 0.75 {
		t.Fatalf("price=%v want 0.75", got)
	}
}

func TestMappedPricingRowOverridesCheaperCanonicalFallback(t *testing.T) {
	mapping := `{"gpt-image-2":"gpt-image-2-official"}`
	candidate := pricedRouteCandidate{ModelMapping: &mapping}

	applyPricedCandidateRow(&candidate, "gpt-image-2", "gpt-image-2", "api", 0.0085, 1)
	applyPricedCandidateRow(&candidate, "gpt-image-2", "gpt-image-2-official", "api", 0.16872, 1)

	if !candidate.HasMappedInputPrice {
		t.Fatal("expected mapped price to be resolved")
	}
	if candidate.InputPrice != 0.16872 {
		t.Fatalf("price=%v want mapped official price 0.16872", candidate.InputPrice)
	}
}

func TestPricedCandidateIgnoresUnrelatedModelRows(t *testing.T) {
	candidate := pricedRouteCandidate{}
	applyPricedCandidateRow(&candidate, "gpt-image-2", "unrelated-cheap-model", "api", 0.00001, 1)
	applyPricedCandidateRow(&candidate, "gpt-image-2", "gpt-image-2", "api", 0.05, 1)
	if candidate.InputPrice != 0.05 {
		t.Fatalf("price=%v want canonical price 0.05", candidate.InputPrice)
	}
}

func TestFreeModelRouteCandidateUsesOnlyDedicatedRoutePrice(t *testing.T) {
	mapping := `{"apimaster-freemodel":"provider/real-free"}`
	candidate := pricedRouteCandidate{ModelMapping: &mapping}

	applyPricedCandidateRow(&candidate, FreeModelID, "provider/real-free", "api", 0.00001, 1)
	applyPricedCandidateRow(&candidate, FreeModelID, FreeModelID, "api", 0.00002, 1)
	applyPricedCandidateRow(&candidate, FreeModelID, FreeModelID, "free_model", 0.001, 1)

	require.True(t, candidate.HasInputPrice)
	require.False(t, candidate.HasMappedInputPrice)
	require.InDelta(t, 0.001, candidate.InputPrice, 0.00000001)
}

func TestFreeModelRoutePriceDoesNotAffectOrdinaryCheapestSelection(t *testing.T) {
	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.ChannelModelPricing{}, &model.Ability{}))

	mapping := `{"apimaster-freemodel":"provider/real-free"}`
	freeChannel := model.Channel{
		Name:         "free-route",
		Key:          "free-key",
		Models:       FreeModelID + ",provider/real-free",
		ModelMapping: &mapping,
		Status:       1,
		Group:        AutoCheapestGroup,
	}
	ordinaryChannel := model.Channel{
		Name:   "ordinary-cheaper",
		Key:    "ordinary-key",
		Models: "provider/real-free",
		Status: 1,
		Group:  AutoCheapestGroup,
	}
	require.NoError(t, db.Create(&freeChannel).Error)
	require.NoError(t, db.Create(&ordinaryChannel).Error)
	require.NoError(t, db.Create(&[]model.ChannelModelPricing{
		{ChannelId: freeChannel.Id, ModelName: FreeModelID, InputPrice: 0.0001, GroupRatio: 1, PricingSource: "free_model"},
		{ChannelId: freeChannel.Id, ModelName: "provider/real-free", InputPrice: 1, GroupRatio: 1, PricingSource: "api"},
		{ChannelId: ordinaryChannel.Id, ModelName: "provider/real-free", InputPrice: 0.5, GroupRatio: 1, PricingSource: "api"},
	}).Error)

	require.Equal(t, ordinaryChannel.Id, selectPricedChannelIDFromDB("provider/real-free", nil, true))
	require.Equal(t, freeChannel.Id, selectPricedChannelIDFromDB(FreeModelID, nil, true))
}

func TestDeepSeekTimedRoutePriceUsesOfficialBaseAndChannelMultipliers(t *testing.T) {
	setting := `{"manual_group_ratio":0.8}`
	candidate := pricedRouteCandidate{
		Setting:             &setting,
		InputPrice:          0.01,
		HasInputPrice:       true,
		GroupRatio:          0.2,
		RechargeRate:        0.5,
		ApimasterPriceRatio: 1.1,
	}
	got, ok := routeCandidateUserInputPriceAt(candidate, "deepseek-v4-flash", 0.44, beijingTime(t, 15, 0))
	if !ok {
		t.Fatal("expected price")
	}
	want := 0.44 * 0.8 * 0.5 * 1.1
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("price=%v want %v", got, want)
	}
}
