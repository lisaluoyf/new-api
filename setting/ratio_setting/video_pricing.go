package ratio_setting

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
)

const VideoModelPricingOption = "VideoModelPricing"
const defaultVideoModelPricing = `{"minimax-h3":{"unit":"second","prices":{"768P":0.08,"2K":0.13}},"kling-v3-omni":{"unit":"second","prices":{"base":0.084,"sound":0.112,"video":0.126,"pro":0.112,"pro-sound":0.14,"pro-video":0.168,"4k":0.5357,"4k-sound":0.5357}}}`

type videoModelPricing struct {
	Prices map[string]float64 `json:"prices"`
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
	base := 0.0
	for _, baseName := range []string{"base", "768P", "std"} {
		if base = videoPriceByName(config.Prices, baseName); base > 0 {
			break
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
