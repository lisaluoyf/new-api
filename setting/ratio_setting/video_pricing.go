package ratio_setting

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
)

const VideoModelPricingOption = "VideoModelPricing"
const defaultVideoModelPricing = `{"minimax-h3":{"unit":"second","prices":{"768P":0.08,"2K":0.13}},"kling-v3-omni":{"unit":"second","prices":{"base":0.084,"sound":0.112,"video":0.126,"pro":0.112,"pro-sound":0.14,"pro-video":0.168,"4k":0.5357,"4k-sound":0.5357}},"doubao-seedance-2.0":{"unit":"second","base_price":0.142,"prices":{"480P":0.066,"480P-input":0.04,"720P":0.142,"720P-input":0.08584,"1080P":0.3544,"1080P-input":0.21568,"4K":0.722,"4K-input":0.44432}}}`

type videoModelPricing struct {
	BasePrice float64            `json:"base_price,omitempty"`
	Prices    map[string]float64 `json:"prices"`
}

func DefaultVideoModelPricingJSON() string { return defaultVideoModelPricing }

func getVideoModelPricing(model string) (videoModelPricing, bool) {
	raw := defaultVideoModelPricing
	common.OptionMapRWMutex.RLock()
	if configured, ok := common.OptionMap[VideoModelPricingOption]; ok && strings.TrimSpace(configured) != "" {
		raw = configured
	}
	common.OptionMapRWMutex.RUnlock()
	var all map[string]videoModelPricing
	if common.Unmarshal([]byte(raw), &all) != nil {
		return videoModelPricing{}, false
	}
	config, ok := all[strings.ToLower(model)]
	if !ok && raw != defaultVideoModelPricing {
		var defaults map[string]videoModelPricing
		if common.Unmarshal([]byte(defaultVideoModelPricing), &defaults) == nil {
			config, ok = defaults[strings.ToLower(model)]
		}
	}
	if !ok {
		return videoModelPricing{}, false
	}
	return config, true
}

func videoPriceByName(prices map[string]float64, name string) float64 {
	for candidate, price := range prices {
		if price > 0 && strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(name)) {
			return price
		}
	}
	return 0
}

// GetVideoModelPriceRatio converts a configured per-second variant price into
// a multiplier over the model's base price. "base" is preferred as the base
// key, followed by the legacy MiniMax "768P" key and then "std".
func GetVideoModelPriceRatio(model, variant string) float64 {
	config, ok := getVideoModelPricing(model)
	if !ok {
		return 1
	}
	base := config.BasePrice
	if base <= 0 {
		for _, baseName := range []string{"base", "768P", "std"} {
			if base = videoPriceByName(config.Prices, baseName); base > 0 {
				break
			}
		}
	}
	selected := videoPriceByName(config.Prices, variant)
	if base <= 0 || selected <= 0 {
		return 1
	}
	return selected / base
}

func GetVideoModelResolutionRatio(model, resolution string) float64 {
	return GetVideoModelPriceRatio(model, resolution)
}

// GetVideoModelBasePrice returns an explicit per-unit calculation base. Older
// media pricing entries omit this field and continue using the regular model or
// channel price as their billing base.
func GetVideoModelBasePrice(model string) (float64, bool) {
	config, ok := getVideoModelPricing(model)
	if !ok || config.BasePrice <= 0 {
		return 0, false
	}
	return config.BasePrice, true
}

func GetVideoModelPrice(model, variant string) (float64, bool) {
	config, ok := getVideoModelPricing(model)
	if !ok {
		return 0, false
	}
	for name, price := range config.Prices {
		if price > 0 && strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(variant)) {
			return price, true
		}
	}
	return 0, false
}
