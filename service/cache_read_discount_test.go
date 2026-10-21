package service

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func cacheDiscountTestInfo(model string, channel int, discount, group float64) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		OriginModelName: model,
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: channel},
		StartTime:       time.Now(),
		PriceData: types.PriceData{
			ModelRatio: 1, CompletionRatio: 2, CacheRatio: 0.02,
			CacheCreationRatio: 1.25,
			GroupRatioInfo:     types.GroupRatioInfo{GroupRatio: group * discount, UserModelDiscount: discount},
		},
	}
}

func TestCacheReadDiscountExactModelAndChannelRatioBilling(t *testing.T) {
	for _, tc := range []struct {
		name, model     string
		channel         int
		discount, group float64
		want            int
	}{
		{"target", "deepseek-flash", 204, 0.8, 1, 570000},
		{"group_price_preserved", "deepseek-flash", 204, 0.8, 2, 1140000},
		{"other_channel", "deepseek-flash", 205, 0.8, 1, 568000},
		{"other_model", "deepseek-v4-flash", 204, 0.8, 1, 568000},
		{"no_discount", "deepseek-flash", 204, 1, 1, 710000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := cacheDiscountTestInfo(tc.model, tc.channel, tc.discount, tc.group)
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			usage := &dto.Usage{PromptTokens: 1000000, CompletionTokens: 100000, PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 500000}}
			summary := calculateTextQuotaSummary(ctx, info, usage)
			require.Equal(t, tc.want, summary.Quota)
			require.Equal(t, 500000, usage.PromptTokensDetails.CachedTokens)
			require.Equal(t, 0.02, info.PriceData.CacheRatio)
		})
	}
}

func TestCacheReadDiscountTieredBilling(t *testing.T) {
	expr := `(weekday("UTC") >= 1 && weekday("UTC") <= 5 && ((hour("UTC") >= 1 && hour("UTC") < 4) || (hour("UTC") >= 6 && hour("UTC") < 10))) ? tier("peak", p * 0.3 + cr * 0.006 + c * 1.2) : tier("off_peak", p * 0.15 + cr * 0.003 + c * 0.6)`
	for _, tc := range []struct {
		name, model     string
		channel, hour   int
		discount, group float64
		p, cr, c        float64
		wantUSD         float64
		tier            string
	}{
		{"peak_mixed", "deepseek-flash", 204, 2, 0.8, 1, 1000000, 1000000, 1000000, 1.206, "peak"},
		{"off_peak_mixed", "deepseek-flash", 204, 5, 0.8, 1, 1000000, 1000000, 1000000, 0.603, "off_peak"},
		{"peak_cache_only", "deepseek-flash", 204, 2, 0.5, 1, 0, 1000000, 0, 0.006, "peak"},
		{"off_peak_cache_only", "deepseek-flash", 204, 5, 0.5, 1, 0, 1000000, 0, 0.003, "off_peak"},
		{"no_cache", "deepseek-flash", 204, 2, 0.8, 1, 1000000, 0, 1000000, 1.2, "peak"},
		{"group_price_preserved", "deepseek-flash", 204, 2, 0.8, 2, 1000000, 1000000, 1000000, 2.412, "peak"},
		{"other_channel", "deepseek-flash", 205, 2, 0.8, 1, 1000000, 1000000, 1000000, 1.2048, "peak"},
		{"other_model", "deepseek-v4-flash", 204, 2, 0.8, 1, 1000000, 1000000, 1000000, 1.2048, "peak"},
		{"no_discount", "deepseek-flash", 204, 2, 1, 1, 1000000, 1000000, 1000000, 1.506, "peak"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := cacheDiscountTestInfo(tc.model, tc.channel, tc.discount, tc.group)
			info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{
				BillingMode: "tiered_expr", ModelName: tc.model, ExprString: expr,
				ExprHash: billingexpr.ExprHashString(expr), GroupRatio: tc.discount * tc.group,
				QuotaPerUnit: common.QuotaPerUnit, EstimatedTier: tc.tier,
				EvaluatedAtUnix: time.Date(2026, 10, 21, tc.hour, 0, 0, 0, time.UTC).Unix(),
			}
			params := billingexpr.TokenParams{P: tc.p, CR: tc.cr, C: tc.c, Len: tc.p + tc.cr}
			ok, quota, result := TryTieredSettle(info, params)
			require.True(t, ok)
			require.NotNil(t, result)
			require.Equal(t, billingexpr.QuotaRound(tc.wantUSD*common.QuotaPerUnit), quota)
			require.Equal(t, quota, result.ActualQuotaAfterGroup)
			require.Equal(t, tc.tier, result.MatchedTier)
			require.False(t, result.CrossedTier)
			require.Equal(t, tc.cr, params.CR)
			require.Equal(t, expr, info.TieredBillingSnapshot.ExprString)
		})
	}
}

func TestCacheReadDiscountFollowsFinalChannelAndDoesNotExemptCacheWrites(t *testing.T) {
	info := cacheDiscountTestInfo("deepseek-flash", 204, 0.8, 1)
	require.Equal(t, 1.25, cacheReadUserDiscountScale(info))
	info.ChannelId = 205
	require.Equal(t, float64(1), cacheReadUserDiscountScale(info))
	info.ChannelId = 204
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	summary := calculateTextQuotaSummary(ctx, info, &dto.Usage{PromptTokens: 1000000, PromptTokensDetails: dto.InputTokenDetails{CachedCreationTokens: 1000000}})
	require.Equal(t, 1000000, summary.Quota) // 1M * 1.25 * 0.8
	require.Equal(t, float64(1), cacheReadUserDiscountScale(nil))
	info.ChannelMeta = nil
	require.Equal(t, float64(1), cacheReadUserDiscountScale(info))
}

func TestCacheReadDiscountAuditMetadata(t *testing.T) {
	info := cacheDiscountTestInfo("deepseek-flash", 204, 0.8, 1)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	other := GenerateTextOtherInfo(ctx, info, 1, 0.8, 2, 1000, 0.025, 0, -1)
	require.Equal(t, true, other["cache_read_user_discount_exempt"])
	info.ChannelId = 205
	other = GenerateTextOtherInfo(ctx, info, 1, 0.8, 2, 1000, 0.02, 0, -1)
	require.NotContains(t, other, "cache_read_user_discount_exempt")
}

func TestCacheReadDiscountPreservesChannelPriceAndProcurementSnapshots(t *testing.T) {
	for _, useScale := range []bool{false, true} {
		info := cacheDiscountTestInfo("deepseek-flash", 204, 0.8, 1)
		expr := `tier("peak", p * 0.3 + cr * 0.006 + c * 1.2)`
		original := &billingexpr.BillingSnapshot{
			BillingMode: "tiered_expr", ModelName: "deepseek-flash", ExprString: expr,
			ExprHash: billingexpr.ExprHashString(expr), QuotaPerUnit: common.QuotaPerUnit,
			GroupRatio: 1, EstimatedTier: "peak",
		}
		if useScale {
			original.PriceScale = billingexpr.TokenPriceScale{Enabled: true, Input: 0.5, Output: 0.5, CacheRead: 0.5, CacheWrite: 0.5}
		} else {
			multiplier := 0.5
			original.AmountMultiplier = &multiplier
		}
		frozen := *original
		info.ProcurementTieredBillingSnapshot = original
		info.WalletTieredBillingSnapshot = original
		info.TieredBillingSnapshot = ApplyUserModelDiscountToBillingSnapshot(original, dto.UserSetting{DefaultModelDiscount: 0.8}, "deepseek-flash")
		params := billingexpr.TokenParams{P: 1000000, CR: 1000000, C: 1000000, Len: 2000000}
		ok, quota, _ := TryTieredSettle(info, params)
		require.True(t, ok)
		require.Equal(t, billingexpr.QuotaRound(0.603*common.QuotaPerUnit), quota)
		require.Equal(t, frozen, *original)
		procurement, err := billingexpr.ComputeTieredQuota(original, params)
		require.NoError(t, err)
		require.Equal(t, billingexpr.QuotaRound(0.753*common.QuotaPerUnit), procurement.ActualQuotaAfterGroup)
	}
}
