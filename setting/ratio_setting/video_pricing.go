package ratio_setting

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
)

const VideoModelPricingOption = "VideoModelPricing"
const defaultVideoModelPricing = `{"minimax-h3":{"unit":"second","prices":{"768P":0.08,"2K":0.13},"official_prices":{"768P":0.08,"2K":0.13}},"kling-v3-omni":{"unit":"second","prices":{"base":0.084,"sound":0.112,"video":0.126,"pro":0.112,"pro-sound":0.14,"pro-video":0.168,"4k":0.5357,"4k-sound":0.5357}},"seedance-2.0":{"unit":"second","base_variant":"720P","base_price":0.1512,"prices":{"480P":0.07031,"480P-input":0.04319,"720P":0.1512,"720P-input":0.09288,"1080P":0.37422,"1080P-input":0.22842,"4K":0.7776,"4K-input":0.46656},"official_prices":{"480P":0.07031,"480P-input":0.04319,"720P":0.1512,"720P-input":0.09288,"1080P":0.37422,"1080P-input":0.22842,"4K":0.7776,"4K-input":0.46656}},"seedance-2.5":{"unit":"second","base_variant":"720P","base_price":0.23112,"prices":{"480P":0.1028,"480P-input":0.06149,"720P":0.23112,"720P-input":0.13824,"1080P":0.56862,"1080P-input":0.3402},"official_prices":{"480P":0.1028,"480P-input":0.06149,"720P":0.23112,"720P-input":0.13824,"1080P":0.56862,"1080P-input":0.3402}},"seedance-2.0-fast":{"unit":"second","base_variant":"720P","base_price":0.12096,"prices":{"480P":0.05625,"480P-input":0.03315,"720P":0.12096,"720P-input":0.07128},"official_prices":{"480P":0.05625,"480P-input":0.03315,"720P":0.12096,"720P-input":0.07128}},"seedance-2.0-mini":{"unit":"second","base_variant":"720P","base_price":0.0756,"prices":{"480P":0.03515,"480P-input":0.02109,"720P":0.0756,"720P-input":0.04536},"official_prices":{"480P":0.03515,"480P-input":0.02109,"720P":0.0756,"720P-input":0.04536}}}`

type videoModelPricing struct {
	Unit           string             `json:"unit,omitempty"`
	BasePrice      float64            `json:"base_price,omitempty"`
	BaseVariant    string             `json:"base_variant,omitempty"`
	Prices         map[string]float64 `json:"prices"`
	OfficialPrices map[string]float64 `json:"official_prices,omitempty"`
}

type VideoModelPricingDetails struct {
	Unit           string
	BasePrice      float64
	BaseVariant    string
	Prices         map[string]float64
	OfficialPrices map[string]float64
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
	modelKey := strings.ToLower(strings.TrimSpace(model))
	config, ok := all[modelKey]
	// Honor an exact configured name first; otherwise bridge pre/post-rename
	// configurations so historical tasks keep their pricing after migration.
	if !ok {
		switch modelKey {
		case "seedance-2.0":
			config, ok = all["doubao-seedance-2.0"]
		case "doubao-seedance-2.0":
			config, ok = all["seedance-2.0"]
		}
	}
	if modelKey == "doubao-seedance-2.0" {
		modelKey = "seedance-2.0"
	}
	var defaults map[string]videoModelPricing
	if raw != defaultVideoModelPricing && common.Unmarshal([]byte(defaultVideoModelPricing), &defaults) == nil {
		defaultConfig, hasDefault := defaults[modelKey]
		if !ok && hasDefault {
			config, ok = defaultConfig, true
		} else if ok && hasDefault {
			config = mergeVideoModelPricingDefaults(config, defaultConfig)
		}
	}
	if !ok {
		return videoModelPricing{}, false
	}
	return config, true
}

func mergeVideoModelPricingDefaults(config, defaults videoModelPricing) videoModelPricing {
	if strings.TrimSpace(config.Unit) == "" {
		config.Unit = defaults.Unit
	}
	if config.BasePrice <= 0 {
		config.BasePrice = defaults.BasePrice
	}
	if strings.TrimSpace(config.BaseVariant) == "" {
		config.BaseVariant = defaults.BaseVariant
	}
	if config.Prices == nil {
		config.Prices = map[string]float64{}
	}
	for name, price := range defaults.Prices {
		if videoPriceByName(config.Prices, name) <= 0 {
			config.Prices[name] = price
		}
	}
	if config.OfficialPrices == nil {
		config.OfficialPrices = map[string]float64{}
	}
	for name, price := range defaults.OfficialPrices {
		if videoPriceByName(config.OfficialPrices, name) <= 0 {
			config.OfficialPrices[name] = price
		}
	}
	return config
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

func GetVideoModelOfficialPriceRatio(model, variant string) float64 {
	config, ok := getVideoModelPricing(model)
	if !ok || len(config.OfficialPrices) == 0 {
		return GetVideoModelPriceRatio(model, variant)
	}
	base, baseVariant := videoBasePrice(config)
	if officialBase := videoPriceByName(config.OfficialPrices, baseVariant); officialBase > 0 {
		base = officialBase
	}
	selected := videoPriceByName(config.OfficialPrices, variant)
	if base <= 0 || selected <= 0 {
		return GetVideoModelPriceRatio(model, variant)
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
	if !ok {
		return 0, false
	}
	base, _ := videoBasePrice(config)
	return base, base > 0
}

func GetVideoModelOfficialBasePrice(model string) (float64, bool) {
	config, ok := getVideoModelPricing(model)
	if !ok {
		return 0, false
	}
	base, baseVariant := videoBasePrice(config)
	if len(config.OfficialPrices) > 0 {
		if official := videoPriceByName(config.OfficialPrices, baseVariant); official > 0 {
			return official, true
		}
		if official := videoPriceByName(config.OfficialPrices, "base"); official > 0 {
			return official, true
		}
		if official := videoPriceByName(config.OfficialPrices, "768P"); official > 0 {
			return official, true
		}
	}
	return base, base > 0
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

func GetVideoModelOfficialPrice(model, variant string) (float64, bool) {
	config, ok := getVideoModelPricing(model)
	if !ok {
		return 0, false
	}
	if price := videoPriceByName(config.OfficialPrices, variant); price > 0 {
		return price, true
	}
	return GetVideoModelPrice(model, variant)
}

func GetVideoModelPricingDetails(model string) (VideoModelPricingDetails, bool) {
	config, ok := getVideoModelPricing(model)
	if !ok || len(config.Prices) == 0 {
		return VideoModelPricingDetails{}, false
	}
	basePrice, baseVariant := videoBasePrice(config)
	if basePrice <= 0 {
		return VideoModelPricingDetails{}, false
	}
	return VideoModelPricingDetails{
		Unit:           config.Unit,
		BasePrice:      basePrice,
		BaseVariant:    baseVariant,
		Prices:         cloneVideoPrices(config.Prices),
		OfficialPrices: cloneVideoPrices(config.OfficialPrices),
	}, true
}

func videoBasePrice(config videoModelPricing) (float64, string) {
	if config.BasePrice > 0 {
		variant := strings.TrimSpace(config.BaseVariant)
		if variant == "" {
			variant = "base"
		}
		return config.BasePrice, variant
	}
	for _, variant := range []string{config.BaseVariant, "base", "768P", "std"} {
		if strings.TrimSpace(variant) == "" {
			continue
		}
		if price := videoPriceByName(config.Prices, variant); price > 0 {
			return price, variant
		}
	}
	return 0, ""
}

func cloneVideoPrices(prices map[string]float64) map[string]float64 {
	cloned := make(map[string]float64, len(prices))
	for name, price := range prices {
		if price > 0 {
			cloned[name] = price
		}
	}
	return cloned
}
