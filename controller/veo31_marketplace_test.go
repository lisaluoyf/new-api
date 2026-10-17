package controller

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestVeoMarketplaceOnlyAdvertisesRouteCapabilities(t *testing.T) {
	for _, tc := range []struct {
		upstream string
		silent   bool
	}{{"veo3.1-fast-official", true}, {"veo3.1-fast", false}, {"veo3.1-fast-ext", false}} {
		t.Run(tc.upstream, func(t *testing.T) {
			mapping := `{"veo-3.1-fast-generate-preview":"` + tc.upstream + `"}`
			prices := func() map[string]float64 {
				return map[string]float64{"720P": .1, "720P-silent": .08, "1080P": .12, "1080P-silent": .1, "4K": .3, "4K-silent": .25}
			}
			view := &VideoMediaPricingView{OfficialPrices: prices(), ProcurementPrices: prices(), BillingPrices: prices()}
			filterVeoMediaPricingForRoute(view, "veo-3.1-fast-generate-preview", &mapping)
			for _, rates := range []map[string]float64{view.OfficialPrices, view.ProcurementPrices, view.BillingPrices} {
				require.Equal(t, .1, rates["720P"])
				_, silent := rates["720P-silent"]
				require.Equal(t, tc.silent, silent)
			}
		})
	}
}
