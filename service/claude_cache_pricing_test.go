package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestClaudeCacheCostIndependentOfRetailRatios(t *testing.T) {
	for _, tc := range []struct {
		name              string
		total, five, hour int
		inclusive         bool
		want              float64
	}{
		{"all one hour", 1000, 0, 1000, false, .002},
		{"mixed with remainder", 1000, 300, 500, false, .001625},
		{"split exceeds aggregate", 0, 300, 500, false, .001375},
		{"five minute", 1000, 1000, 0, false, .00125},
		{"legacy unsplit", 1000, 0, 0, false, .00125},
		{"inclusive input", 1000, 0, 1000, true, .002},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := ConsumeAccountingInput{ModelName: "claude-opus-5-5", CacheWriteTokens: tc.total, CacheWriteTokens5m: tc.five, CacheWriteTokens1h: tc.hour, InputTokensIncludeCache: tc.inclusive}
			if tc.inclusive {
				input.InputTokens = 1000
			}
			require.InDelta(t, tc.want, amountUSD(accountingPriceTuple{InputPrice: 1, CacheCreationPrice: 1.25}, input), 1e-12)
			zero := 0.0
			require.InDelta(t, 1.25*float64(normalizedAccountingCacheWriteTotal(input)-tc.hour)/1e6, amountUSD(accountingPriceTuple{InputPrice: 1, CacheCreationPrice: 1.25, CacheCreationPrice1h: &zero}, input), 1e-12)
		})
	}
}

func TestClaudeCacheExplicitPricePersistsAndQuotaStaysSettled(t *testing.T) {
	old := model.DB
	t.Cleanup(func() { model.DB = old })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.ChannelModelPricing{}, &model.User{}))
	one := 1.0
	hour := 3.0
	require.NoError(t, db.Create(&model.Channel{Id: 92101, RechargeRate: &one, ApimasterPriceRatio: &one}).Error)
	require.NoError(t, db.Create(&model.User{Id: 92101, Username: "ttl-cache-test"}).Error)
	require.NoError(t, model.UpsertChannelModelPricings([]model.ChannelModelPricing{{ChannelId: 92101, ModelName: "claude-opus-5-5", InputPrice: 1, OutputPrice: 5, CachePrice: .05, CacheCreationPrice: 1.25, CacheCreationPrice1h: &hour, GroupRatio: 1}}))
	stored, err := model.GetChannelModelPricing(92101, "claude-opus-5-5")
	require.NoError(t, err)
	require.NotNil(t, stored.CacheCreationPrice1h)
	require.Equal(t, 3.0, *stored.CacheCreationPrice1h)
	input := ConsumeAccountingInput{UserId: 92101, ChannelId: 92101, ModelName: "claude-opus-5-5", CacheWriteTokens: 1000, CacheWriteTokens1h: 1000, GroupRatio: 1, Quota: 1234}
	input.UseQuotaForUserAmounts = true
	got := BuildConsumeAccountingFields(input)
	require.InDelta(t, 1234/common.QuotaPerUnit, got.UserFinalAmountUSD, 1e-12)
	require.InDelta(t, .003, got.ChannelCostAmountUSD, 1e-12)
	var snap consumeAccountingSnapshot
	require.NoError(t, common.UnmarshalJsonStr(got.Snapshot, &snap))
	require.Equal(t, 1000, snap.Tokens["cache_write_1h"])
}

func TestClaudeLegacyCacheMultiplierAppliedOnlyOnce(t *testing.T) {
	multiplier := 1.6
	require.InDelta(t, .002, amountUSD(accountingPriceTuple{CacheCreationPrice: 1.25}, ConsumeAccountingInput{CacheWriteTokens: 1000, CacheWritePriceMultiplier: &multiplier}), 1e-12)
}
