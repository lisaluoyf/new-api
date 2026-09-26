package service

import (
	"bufio"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"math"
	"os"
	"testing"
	"time"
)

const auditGPTExpr = `len <= 272000 ? tier("standard", p*10+c*50+cr+cc*12.5) : tier("long_context", p*20+c*75+cr*2+cc*25)`

func TestTieredChannelAccountingUsesCompleteScheduleAndFrozenPrices(t *testing.T) {
	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.ChannelModelPricing{}, &model.User{}))
	one, recharge, markup := 1.0, 0.148869, 2.0
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "tier-audit"}).Error)
	require.NoError(t, db.Create(&model.Channel{Id: 258, RechargeRate: &recharge, ApimasterPriceRatio: &markup}).Error)
	require.NoError(t, db.Create(&model.ChannelModelPricing{ChannelId: 258, ModelName: "audit-tier-model", InputPrice: 1.5, OutputPrice: 7.5, CachePrice: .15, CacheCreationPrice: 1.875, GroupRatio: .15, BillingMode: "tiered_expr", BillingExpr: auditGPTExpr}).Error)
	at := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	proc, err := ResolveTokenBillingExpression(258, "audit-tier-model", true, at)
	require.NoError(t, err)
	wallet, err := ResolveTokenBillingExpression(258, "audit-tier-model", false, at)
	require.NoError(t, err)
	// Upstream 321773 input / 418 output, same as the investigated long request.
	input := ConsumeAccountingInput{UserId: 1, ChannelId: 258, ModelName: "audit-tier-model", InputTokens: 321773, OutputTokens: 418, InputTokensIncludeCache: true, GroupRatio: one, ProcurementTieredSnapshot: proc, WalletTieredSnapshot: wallet}
	want := (321773.0*20 + 418*75) / 1e6 * .15 * recharge
	got := BuildConsumeAccountingFields(input)
	require.NotContains(t, got.Snapshot, "tiered_accounting_failed")
	require.InDelta(t, want, got.ChannelCostAmountUSD, 1e-12)
	require.InDelta(t, want*markup, got.UserPriceAmountUSD, 1e-12)
	// A concurrent pricing refresh must not reprice an in-flight request.
	require.NoError(t, db.Model(&model.ChannelModelPricing{}).Where("channel_id = ?", 258).Updates(map[string]any{"group_ratio": .99, "input_price": 9.9}).Error)
	got = BuildConsumeAccountingFields(input)
	require.InDelta(t, want, got.ChannelCostAmountUSD, 1e-12)
	input.UseQuotaForUserAmounts = true
	input.Quota = 12345
	got = BuildConsumeAccountingFields(input)
	require.InDelta(t, 12345/common.QuotaPerUnit, got.UserFinalAmountUSD, 1e-12)
}

func TestTieredCacheDimensionsDoNotChangeTierOrLoseWrites(t *testing.T) {
	for _, expr := range []string{auditGPTExpr, `len<=272000?tier("standard",p*10+c*50):tier("long_context",p*20+c*75)`, `len<=272000?tier("standard",p*10+cc*12.5+cc1h*20):tier("long_context",p*20+cc*25+cc1h*40)`} {
		inclusive := &dto.Usage{PromptTokens: 300000, CompletionTokens: 100}
		inclusive.PromptTokensDetails.CachedTokens = 250000
		inclusive.PromptTokensDetails.CachedCreationTokens = 10000
		exclusive := *inclusive
		exclusive.PromptTokens = 40000
		exclusive.UsageSemantic = "anthropic"
		// Aggregate write count without TTL split must not disappear.
		p1 := BuildTieredTokenParams(inclusive, true, billingexpr.UsedVars(expr))
		p2 := BuildTieredTokenParams(&exclusive, false, billingexpr.UsedVars(expr))
		v1, tr1, e := billingexpr.RunExpr(expr, p1)
		require.NoError(t, e)
		v2, tr2, e := billingexpr.RunExpr(expr, p2)
		require.NoError(t, e)
		require.Equal(t, "long_context", tr1.MatchedTier)
		require.Equal(t, tr1.MatchedTier, tr2.MatchedTier)
		require.InDelta(t, v1, v2, 1e-8)
	}
}

func TestTieredProductionConfigurationAudit(t *testing.T) {
	file := os.Getenv("TIER_AUDIT_INPUT")
	if file == "" {
		t.Skip("read-only production configuration replay")
	}
	f, err := os.Open(file)
	require.NoError(t, err)
	defer f.Close()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 4096), 4<<20)
	count, media := 0, 0
	for scan.Scan() {
		var row struct {
			ChannelID  int    `json:"channel_id"`
			Model      string `json:"model"`
			Expression string `json:"billing_expr"`
		}
		require.NoError(t, common.Unmarshal(scan.Bytes(), &row))
		if !IsTokenBillingExpression(row.Expression) {
			media++
			continue
		}
		count++
		t.Run(fmt.Sprintf("%d_%s", row.ChannelID, row.Model), func(t *testing.T) {
			_, e := billingexpr.CompileFromCache(row.Expression)
			require.NoError(t, e)
			for _, length := range []int{0, 1000, 31999, 32000, 32001, 128000, 128001, 200000, 200001, 272000, 272001, 512000, 512001, 1000000} {
				for _, cache := range []int{0, length * 9 / 10} {
					u := &dto.Usage{PromptTokens: length, CompletionTokens: 199}
					u.PromptTokensDetails.CachedTokens = cache
					params := BuildTieredTokenParams(u, true, billingexpr.UsedVars(row.Expression))
					v, _, e := billingexpr.RunExprWithRequestAt(row.Expression, params, billingexpr.RequestInput{}, time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC))
					require.NoError(t, e)
					require.False(t, math.IsNaN(v) || math.IsInf(v, 0))
					require.GreaterOrEqual(t, v, 0.0)
					require.Equal(t, float64(length), params.Len)
				}
			}
		})
	}
	require.NoError(t, scan.Err())
	t.Logf("audited %d token configurations; %d media/task configurations routed separately", count, media)
}

func TestTokenExpressionClassification(t *testing.T) {
	require.False(t, IsTokenBillingExpression(`tier("p", fixed(1.6))`))
	require.False(t, IsTokenBillingExpression(`tier("image",param("p")*1000000)`))
	require.True(t, IsTokenBillingExpression(auditGPTExpr))
}
