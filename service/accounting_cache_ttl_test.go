package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCacheTTLAccountingUsesSettlementWeights(t *testing.T) {
	prices := accountingPriceTuple{InputPrice: 1.187736, OutputPrice: 5.93868, CachePrice: .1187736, CacheCreationPrice: 1.48467}
	for _, tc := range []struct {
		name                    string
		total, fiveMin, oneHour int
		wantWriteUSD            float64
	}{
		{"production one hour regression", 18237, 0, 18237, .043321482864},
		{"mixed TTL and unsplit remainder", 1000, 300, 500, .001930071},
		{"split exceeds aggregate", 0, 300, 500, .001633137},
		{"five minute unchanged", 1000, 1000, 0, .00148467},
		{"unsplit unchanged", 1000, 0, 0, .00148467},
	} {
		t.Run(tc.name, func(t *testing.T) {
			summary := textQuotaSummary{CacheCreationTokens: tc.total, CacheCreationTokens5m: tc.fiveMin, CacheCreationTokens1h: tc.oneHour, CacheCreationRatio: 1.25, CacheCreationRatio5m: 1.25, CacheCreationRatio1h: 2}
			input := ConsumeAccountingInput{InputTokens: 14, OutputTokens: 4, CacheWriteTokens: cacheWriteTokensTotal(summary), CacheWriteTokens5m: tc.fiveMin, CacheWriteTokens1h: tc.oneHour, CacheWritePriceMultiplier: cacheWriteAccountingMultiplier(summary), GroupRatio: 1.05}
			want := (14*prices.InputPrice+4*prices.OutputPrice)/1e6 + tc.wantWriteUSD
			require.InDelta(t, want, amountUSD(prices, input), 1e-12)
			base, final := userAmountsUSD(prices, input)
			require.InDelta(t, want, base, 1e-12)
			require.InDelta(t, want*1.05, final, 1e-12)
			input.ZeroUserCharge = true
			base, final = userAmountsUSD(prices, input)
			require.Zero(t, base)
			require.Zero(t, final)
		})
	}
}

func TestCacheTTLAccountingSnapshotRecordsSplitAndWeightedAmounts(t *testing.T) {
	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.ChannelModelPricing{}, &model.User{}))
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "cache-ttl-accounting"}).Error)
	settings := `{"manual_group_ratio":1}`
	one := 1.0
	require.NoError(t, db.Create(&model.Channel{Id: 901, Setting: &settings, RechargeRate: &one, ApimasterPriceRatio: &one}).Error)
	require.NoError(t, model.UpsertChannelModelPricings([]model.ChannelModelPricing{{ChannelId: 901, ModelName: "claude-opus-5", InputPrice: 1, OutputPrice: 5, CachePrice: .1, CacheCreationPrice: 1.25, GroupRatio: 1}}))
	summary := textQuotaSummary{CacheCreationTokens: 1000, CacheCreationTokens1h: 1000, CacheCreationRatio: 1.25, CacheCreationRatio1h: 2}
	got := BuildConsumeAccountingFields(ConsumeAccountingInput{UserId: 1, ChannelId: 901, ModelName: "claude-opus-5", CacheWriteTokens: 1000, CacheWriteTokens1h: 1000, CacheWritePriceMultiplier: cacheWriteAccountingMultiplier(summary), GroupRatio: 1.05, Quota: 1050})
	require.InDelta(t, .002, got.ChannelCostAmountUSD, 1e-12)
	require.InDelta(t, .0021, got.UserFinalAmountUSD, 1e-12)
	var snap consumeAccountingSnapshot
	require.NoError(t, common.UnmarshalJsonStr(got.Snapshot, &snap))
	require.Equal(t, 1000, snap.Tokens["cache_write_1h"])
	require.Equal(t, "mixed_billing_v2_cache_ttl", snap.AccountingAmountVersion)
	require.Equal(t, 1.6, snap.Prices["cache_write_price_multiplier"])
}

func TestCacheTTLAccountingKeepsQuotaAndInclusiveSemantics(t *testing.T) {
	summary := textQuotaSummary{CacheCreationTokens: 1000, CacheCreationTokens1h: 1000, CacheCreationRatio: 1.25, CacheCreationRatio1h: 2, InputTokensIncludeCache: true}
	require.Nil(t, cacheWriteAccountingMultiplier(summary))
	summary.InputTokensIncludeCache = false
	input := ConsumeAccountingInput{Quota: 12345, GroupRatio: 1.05, UseQuotaForUserAmounts: true, CacheWriteTokens: 1000, CacheWritePriceMultiplier: cacheWriteAccountingMultiplier(summary)}
	base, final := userAmountsUSD(accountingPriceTuple{CacheCreationPrice: 1.25}, input)
	require.InDelta(t, float64(input.Quota)/common.QuotaPerUnit, final, 1e-12)
	require.InDelta(t, final/1.05, base, 1e-12)
}
