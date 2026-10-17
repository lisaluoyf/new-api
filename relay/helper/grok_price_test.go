package helper

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

func TestGrokOfficialPrechargeThenActualTier(t *testing.T) {
	oldDB := model.DB
	model.DB = nil
	t.Cleanup(func() { model.DB = oldDB })
	for _, name := range []string{"grok-4.5", "grok-4.6", "grok-4.7"} {
		for _, prompt := range []int{199999, 200000, 200001} {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Set("group", "default")
			ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{OriginModelName: name, UserGroup: "default", UsingGroup: "default", BillingRequestInput: &billingexpr.RequestInput{Body: []byte(`{"model":"` + name + `"}`)}}
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
