package ratio_setting

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"math"
	"strings"
)

// ValidateOmniOfficialVideoPricing keeps the four official list-price tiers
// complete and the 720p calculation base consistent when an operator saves them.
func ValidateOmniOfficialVideoPricing(raw string) error {
	var values map[string]struct {
		Unit           string             `json:"unit"`
		BaseVariant    string             `json:"base_variant"`
		BasePrice      float64            `json:"base_price"`
		Prices         map[string]float64 `json:"prices"`
		OfficialPrices map[string]float64 `json:"official_prices"`
	}
	if err := common.UnmarshalJsonStr(raw, &values); err != nil || values == nil {
		return fmt.Errorf("invalid video pricing configuration")
	}
	entry, exists := values["gemini-omni-1.1-flash"]
	if !exists {
		return nil
	}
	for _, key := range []string{"360P", "720P", "1080P", "4K"} {
		price, official := entry.Prices[key], entry.OfficialPrices[key]
		if price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) || official <= 0 || math.IsNaN(official) || math.IsInf(official, 0) || math.Abs(price-official) > 1e-12 {
			return fmt.Errorf("Gemini Omni requires matching positive official prices for 360P, 720P, 1080P and 4K")
		}
	}
	if entry.Unit != "second" || !strings.EqualFold(entry.BaseVariant, "720P") || math.IsNaN(entry.BasePrice) || math.IsInf(entry.BasePrice, 0) || math.Abs(entry.BasePrice-entry.Prices["720P"]) > 1e-12 {
		return fmt.Errorf("Gemini Omni base price must equal its official 720P price")
	}
	return nil
}
