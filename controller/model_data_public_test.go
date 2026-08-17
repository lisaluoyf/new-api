package controller

import (
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
