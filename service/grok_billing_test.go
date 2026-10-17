package service

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
	"time"
)

// Independent price vectors check the published schedule, rather than deriving
// expected amounts from the implementation's expression or ratio settings.
func TestGrokOfficialUsageAndSettlementMatrix(t *testing.T) {
	for _, name := range []string{"grok-4.5", "grok-4.6", "grok-4.7"} {
		expr, ok := billing_setting.GetBillingExpr(name)
		require.True(t, ok)
		for _, prompt := range []int{0, 1000, 199999, 200000, 200001, 499999, 500000} {
			for _, cached := range []int{0, prompt / 2, prompt} {
				for _, output := range []int{0, 1, 1000} {
					for _, discount := range []float64{0, .37, 1, 1.05} {
						label := fmt.Sprintf("%s/p%d/cache%d/o%d/ratio%g", name, prompt, cached, output, discount)
						t.Run(label, func(t *testing.T) {
							u := &dto.Usage{PromptTokens: prompt, CompletionTokens: output}
							u.PromptTokensDetails.CachedTokens = cached
							params := BuildTieredTokenParams(u, true, billingexpr.UsedVars(expr))
							require.Equal(t, float64(prompt), params.Len)
							require.Equal(t, float64(prompt-cached), params.P)
							inputPrice, outputPrice, cachePrice, tier := 2.0, 6.0, .5, "short_context"
							if name == "grok-4.5" {
								cachePrice = .3
							}
							if prompt >= 200000 {
								inputPrice *= 2
								outputPrice *= 2
								cachePrice *= 2
								tier = "long_context"
							}
							usd := (float64(prompt-cached)*inputPrice + float64(cached)*cachePrice + float64(output)*outputPrice) / 1e6
							snap := &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ModelName: name, ExprString: expr, ExprHash: billingexpr.ExprHashString(expr), QuotaPerUnit: common.QuotaPerUnit, GroupRatio: discount, EstimatedTier: "short_context"}
							info := &relaycommon.RelayInfo{TieredBillingSnapshot: snap}
							applied, quota, result := TryTieredSettle(info, params)
							require.True(t, applied)
							require.NotNil(t, result)
							require.Equal(t, tier, result.MatchedTier)
							require.InDelta(t, usd, result.ActualQuotaBeforeGroup/common.QuotaPerUnit, 1e-12)
							require.Equal(t, billingexpr.QuotaRound(usd*common.QuotaPerUnit*discount), quota)
							require.Equal(t, tier != "short_context", result.CrossedTier)
						})
					}
				}
			}
		}
	}
}

func TestGrokOfficialWalletTokenDebitAndLogs(t *testing.T) {
	for _, name := range []string{"grok-4.5", "grok-4.6", "grok-4.7"} {
		for _, prompt := range []int{199999, 200000, 200001} {
			for _, stream := range []bool{false, true} {
				for _, precharge := range []int{1000, 500000} {
					t.Run(fmt.Sprintf("%s/%d/stream=%t/precharge=%d", name, prompt, stream, precharge), func(t *testing.T) {
						truncate(t)
						const initial = 10000000
						seedUser(t, 1, initial)
						seedToken(t, 2, 1, "grok-tier-test-key", initial)
						require.NoError(t, model.DB.Create(&model.Channel{Id: 94, Name: "grok-tier-test"}).Error)
						ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
						ctx.Set("token_quota", initial)
						ctx.Set("token_name", "grok-tier-test")
						ctx.Set(common.RequestIdKey, "grok-tier-wallet-test")
						expr, _ := billing_setting.GetBillingExpr(name)
						snap := &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ModelName: name, ExprString: expr, ExprHash: billingexpr.ExprHashString(expr), QuotaPerUnit: common.QuotaPerUnit, GroupRatio: .37, EstimatedTier: "short_context"}
						info := &relaycommon.RelayInfo{RequestId: "grok-tier-wallet-test", UserId: 1, UserQuota: initial, TokenId: 2, TokenKey: "grok-tier-test-key", TokenGroup: "default", UsingGroup: "default", OriginModelName: name, StartTime: time.Now(), ForcePreConsume: true, IsStream: stream, UserSetting: dto.UserSetting{BillingPreference: "wallet_only"}, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 94}, PriceData: types.PriceData{GroupRatioInfo: types.GroupRatioInfo{GroupRatio: .37}}}
						require.Nil(t, PreConsumeBilling(ctx, precharge, info))
						info.TieredBillingSnapshot = snap
						info.WalletTieredBillingSnapshot = snap
						info.PriceDataSource = "wallet"
						usage := &dto.Usage{PromptTokens: prompt, CompletionTokens: 1000, TotalTokens: prompt + 1000, UsageSource: "upstream_reported"}
						usage.PromptTokensDetails.CachedTokens = 150000
						in, out, cache := 2.0, 6.0, .5
						if name == "grok-4.5" {
							cache = .3
						}
						if prompt >= 200000 {
							in *= 2
							out *= 2
							cache *= 2
						}
						usd := (float64(prompt-150000)*in + 150000*cache + 1000*out) / 1e6
						want := billingexpr.QuotaRound(usd * common.QuotaPerUnit * .37)
						require.NoError(t, PostTextConsumeQuota(ctx, info, usage, nil))
						require.NoError(t, SettleBilling(ctx, info, want)) // repeated settlement must not debit twice
						var user model.User
						var token model.Token
						var log model.Log
						require.NoError(t, model.DB.First(&user, 1).Error)
						require.NoError(t, model.DB.First(&token, 2).Error)
						require.Equal(t, initial-want, user.Quota)
						require.Equal(t, initial-want, token.RemainQuota)
						require.NoError(t, model.LOG_DB.Where("request_id = ?", info.RequestId).Order("id desc").First(&log).Error)
						require.Equal(t, want, log.Quota)
						require.InDelta(t, float64(want)/common.QuotaPerUnit, log.AccountingUserFinalAmountUSD, 1e-9)
						require.Contains(t, log.Other, `"billing_mode":"tiered_expr"`)
						tier := "short_context"
						if prompt >= 200000 {
							tier = "long_context"
						}
						require.Contains(t, log.Other, `"matched_tier":"`+tier+`"`)
					})
				}
			}
		}
	}
}
