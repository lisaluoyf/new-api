package controller

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/channel/task/apimartomni"
	"strings"
)

// Keep Google's verified 720p baseline separate from route-specific prices.
// Ext task tiers are expressed as effective USD/second for their stated length.
func filterOmniMediaPricingForRoute(view *VideoMediaPricingView, mapping *string) {
	var names map[string]string
	if mapping != nil {
		_ = common.UnmarshalJsonStr(*mapping, &names)
	}
	ext := names[apimartomni.Model] == apimartomni.Ext
	goldenBase := apimartomni.OfficialGoldBase
	if ext {
		goldenBase = apimartomni.ExtGoldBase
	}
	procurementScale := view.ProcurementPrices["720P"] / goldenBase
	retailScale := view.BillingPrices["720P"] / goldenBase
	if procurementScale <= 0 || retailScale <= 0 {
		return
	}
	view.ProcurementPrices = map[string]float64{}
	view.BillingPrices = map[string]float64{}
	put := func(key string, gold float64) {
		view.ProcurementPrices[key] = gold * procurementScale
		view.BillingPrices[key] = gold * retailScale
	}
	for _, res := range []string{"360p", "720p", "1080p", "4k"} {
		if ext {
			for _, n := range []int{4, 6, 8, 10} {
				put(fmt.Sprintf("%s-%ds", strings.ToUpper(res), n), apimartomni.GoldenPriceUSD(res, n, true, false)/float64(n))
			}
			put(strings.ToUpper(res)+"-reference", apimartomni.GoldenPriceUSD(res, 1, true, true))
		} else {
			put(strings.ToUpper(res), apimartomni.GoldenPriceUSD(res, 1, false, false))
		}
	}
	if ext {
		view.BaseVariant = "720P-6s"
		view.OfficialPrices = map[string]float64{}
	}
}
