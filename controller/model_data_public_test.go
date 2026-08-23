package controller

import (
	"math"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
)

func TestPublicMarketplaceItemDoesNotExposeInternalChannelData(t *testing.T) {
	price := 1.25
	item := PublicMarketplaceItem{
		ChannelID:             7,
		UserPrice:             &price,
		ActualOutputUserPrice: &price,
		FingerprintHistory: []PublicDetectPoint{
			{Status: "pass", DetectTime: 123},
		},
	}

	payload, err := common.Marshal(item)
	if err != nil {
		t.Fatalf("marshal public marketplace item: %v", err)
	}

	jsonText := string(payload)
	for _, forbidden := range []string{
		"channel_name",
		"key_group",
		"group_name",
		"input_price",
		"output_price",
		"actual_price",
		"actual_output_price",
		"recharge_rate",
	} {
		if strings.Contains(jsonText, `"`+forbidden+`"`) {
			t.Fatalf("public marketplace response exposed %q: %s", forbidden, jsonText)
		}
	}
}

func TestBuildVideoMediaPricingViewUsesEightSeedanceTiers(t *testing.T) {
	view := buildVideoMediaPricingView("doubao-seedance-2.0", 0.8)
	if view == nil {
		t.Fatal("expected Seedance media pricing")
	}
	if len(view.OfficialPrices) != 8 || len(view.ProcurementPrices) != 8 || len(view.BillingPrices) != 8 {
		t.Fatalf("unexpected tier counts: official=%d procurement=%d billing=%d", len(view.OfficialPrices), len(view.ProcurementPrices), len(view.BillingPrices))
	}
	if got := view.OfficialPrices["720P"]; got != 0.1775 {
		t.Fatalf("official 720P=%v", got)
	}
	if got := view.ProcurementPrices["720P"]; math.Abs(got-0.142*0.8) > 1e-9 {
		t.Fatalf("procurement 720P=%v", got)
	}
	if got := view.BillingPrices["720P-input"]; got != 0.08584 {
		t.Fatalf("billing 720P-input=%v", got)
	}
}
