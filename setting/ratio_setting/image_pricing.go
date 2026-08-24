package ratio_setting

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
)

const ImageModelPricingOption = "ImageModelPricing"

const defaultImageModelPricing = `{"gpt-image-2":{"unit":"image","base_price":0.25,"base_variant":"1K","prices":{"1K":0.25,"2K":0.3,"4K":0.6}}}`

type imageModelPricing struct {
	Unit        string             `json:"unit,omitempty"`
	BasePrice   float64            `json:"base_price,omitempty"`
	BaseVariant string             `json:"base_variant,omitempty"`
	Prices      map[string]float64 `json:"prices"`
}

type ImageModelPricingDetails struct {
	Unit        string
	BasePrice   float64
	BaseVariant string
	Prices      map[string]float64
}

func DefaultImageModelPricingJSON() string { return defaultImageModelPricing }

func getImageModelPricing(model string) (imageModelPricing, bool) {
	raw := defaultImageModelPricing
	common.OptionMapRWMutex.RLock()
	if configured, ok := common.OptionMap[ImageModelPricingOption]; ok && strings.TrimSpace(configured) != "" {
		raw = configured
	}
	common.OptionMapRWMutex.RUnlock()

	var all map[string]imageModelPricing
	if common.Unmarshal([]byte(raw), &all) != nil {
		return imageModelPricing{}, false
	}
	config, ok := all[strings.ToLower(strings.TrimSpace(model))]
	if !ok {
		for name, candidate := range all {
			if strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(model)) {
				config, ok = candidate, true
				break
			}
		}
	}
	if !ok {
		return imageModelPricing{}, false
	}
	if strings.TrimSpace(config.Unit) == "" {
		config.Unit = "image"
	}
	if strings.TrimSpace(config.BaseVariant) == "" {
		config.BaseVariant = "1K"
	}
	if config.BasePrice <= 0 {
		config.BasePrice = imagePriceByName(config.Prices, config.BaseVariant)
	}
	return config, config.BasePrice > 0
}

func imagePriceByName(prices map[string]float64, name string) float64 {
	for candidate, price := range prices {
		if price > 0 && strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(name)) {
			return price
		}
	}
	return 0
}

func GetImageModelBasePrice(model string) (float64, bool) {
	config, ok := getImageModelPricing(model)
	if !ok || config.BasePrice <= 0 {
		return 0, false
	}
	return config.BasePrice, true
}

func GetImageModelPrice(model, variant string) (float64, bool) {
	config, ok := getImageModelPricing(model)
	if !ok {
		return 0, false
	}
	price := imagePriceByName(config.Prices, variant)
	if price <= 0 {
		return config.BasePrice, config.BasePrice > 0
	}
	return price, true
}

func GetImageModelPriceRatio(model, variant string) float64 {
	config, ok := getImageModelPricing(model)
	if !ok || config.BasePrice <= 0 {
		return 1
	}
	price := imagePriceByName(config.Prices, variant)
	if price <= 0 {
		return 1
	}
	return price / config.BasePrice
}

func GetImageModelPricingDetails(model string) (ImageModelPricingDetails, bool) {
	config, ok := getImageModelPricing(model)
	if !ok || config.BasePrice <= 0 || len(config.Prices) == 0 {
		return ImageModelPricingDetails{}, false
	}
	return ImageModelPricingDetails{
		Unit:        config.Unit,
		BasePrice:   config.BasePrice,
		BaseVariant: config.BaseVariant,
		Prices:      config.Prices,
	}, true
}
