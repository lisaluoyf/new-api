package controller

import (
	"github.com/QuantumNous/new-api/relay/channel/task/apimartomni"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestOmniRoutePriceCardsMatchGoldenTariffs(t *testing.T) {
	for _, ext := range []bool{false, true} {
		base := .088
		up := apimartomni.Model
		if ext {
			base = .05
			up = apimartomni.Ext
		}
		mapping := `{"gemini-omni-1.1-flash":"` + up + `"}`
		view := &VideoMediaPricingView{OfficialPrices: map[string]float64{"720P": .10136}, ProcurementPrices: map[string]float64{"720P": base}, BillingPrices: map[string]float64{"720P": base * 1.1}}
		filterOmniMediaPricingForRoute(view, &mapping)
		count := 4
		if ext {
			count = 20
		}
		require.Len(t, view.BillingPrices, count)
		for key, cost := range view.ProcurementPrices {
			require.InDelta(t, cost*1.1, view.BillingPrices[key], 1e-10)
		}
		if ext {
			require.Empty(t, view.OfficialPrices)
			require.InDelta(t, .15, view.ProcurementPrices["360P-4s"]*4, 1e-10)
			require.InDelta(t, .9, view.ProcurementPrices["4K-10s"]*10, 1e-10)
			require.InDelta(t, .24, view.ProcurementPrices["4K-reference"], 1e-10)
		} else {
			require.InDelta(t, .264, view.ProcurementPrices["4K"], 1e-10)
			require.Equal(t, .10136, view.OfficialPrices["720P"])
		}
	}
}
