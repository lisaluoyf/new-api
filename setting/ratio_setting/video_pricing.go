package ratio_setting

import (
	"encoding/json"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

const VideoModelPricingOption = "VideoModelPricing"
const defaultVideoModelPricing = `{"minimax-h3":{"unit":"second","prices":{"768P":0.08,"2K":0.13}}}`

type videoModelPricing struct {
	Prices map[string]float64 `json:"prices"`
}

func DefaultVideoModelPricingJSON() string { return defaultVideoModelPricing }

func GetVideoModelResolutionRatio(model, resolution string) float64 {
	raw := defaultVideoModelPricing
	common.OptionMapRWMutex.RLock()
	if configured, ok := common.OptionMap[VideoModelPricingOption]; ok && strings.TrimSpace(configured) != "" {
		raw = configured
	}
	common.OptionMapRWMutex.RUnlock()
	var all map[string]videoModelPricing
	if json.Unmarshal([]byte(raw), &all) != nil {
		return 1
	}
	config, ok := all[strings.ToLower(model)]
	if !ok {
		return 1
	}
	base, selected := 0.0, 0.0
	for name, price := range config.Prices {
		if price > 0 && (base == 0 || strings.EqualFold(name, "768P")) {
			base = price
		}
		if strings.EqualFold(name, resolution) {
			selected = price
		}
	}
	if base <= 0 || selected <= 0 {
		return 1
	}
	return selected / base
}
