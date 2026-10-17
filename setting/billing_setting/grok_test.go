package billing_setting

import (
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestGrokOfficialDefaultsAndOverrides(t *testing.T) {
	oldModes, oldExprs := billingSetting.BillingMode, billingSetting.BillingExpr
	billingSetting.BillingMode = map[string]string{}
	billingSetting.BillingExpr = map[string]string{}
	t.Cleanup(func() { billingSetting.BillingMode, billingSetting.BillingExpr = oldModes, oldExprs })
	for _, name := range []string{"grok-4.5", "grok-4.6", "grok-4.7"} {
		expr, ok := GetBillingExpr(name)
		require.True(t, ok)
		require.Equal(t, BillingModeTieredExpr, GetBillingMode(name))
		require.Equal(t, expr, GetBillingExprCopy()[name])
		require.Equal(t, BillingModeTieredExpr, GetBillingModeCopy()[name])
		aliasExpr, ok := GetBillingExpr("x-ai/" + name)
		require.True(t, ok)
		require.Equal(t, expr, aliasExpr)
		require.Equal(t, BillingModeTieredExpr, GetBillingMode("x-ai/"+name))
		require.NoError(t, SmokeTestExpr(expr))
		for _, length := range []float64{199999, 200000, 200001, 500000} {
			cache := .5
			if name == "grok-4.5" {
				cache = .3
			}
			input, output, tier := 2.0, 6.0, "short_context"
			if length >= 200000 {
				input *= 2
				output *= 2
				cache *= 2
				tier = "long_context"
			}
			cost, trace, err := billingexpr.RunExpr(expr, billingexpr.TokenParams{P: 100, CR: length - 100, C: 1000, Len: length})
			require.NoError(t, err)
			require.Equal(t, tier, trace.MatchedTier)
			require.InDelta(t, 100*input+(length-100)*cache+1000*output, cost, 1e-8)
		}
	}
	for _, unaffected := range []string{"grok-4", "grok-4.1-fast", "grok-4.6-fast", "grok-4.7-preview", "grok-imagine-image-2.0", "grok-imagine-video-1.5"} {
		_, found := GrokOfficialBillingExpr(unaffected)
		require.False(t, found)
		require.Equal(t, BillingModeRatio, GetBillingMode(unaffected))
	}
	billingSetting.BillingMode["grok-4.5"] = BillingModeRatio
	billingSetting.BillingExpr["grok-4.5"] = `tier("custom", p)`
	require.Equal(t, BillingModeRatio, GetBillingMode("x-ai/grok-4.5"))
	expr, _ := GetBillingExpr("x-ai/grok-4.5")
	require.Equal(t, `tier("custom", p)`, expr)
	// Copies must not mutate defaults.
	copy := GetBillingExprCopy()
	copy["grok-4.6"] = "broken"
	expr, _ = GetBillingExpr("grok-4.6")
	require.Equal(t, Grok46BillingExpr, expr)
}
