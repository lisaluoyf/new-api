package model

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"strings"
)

// applyVeo31Pricing exposes the same per-second resolution/audio ratios used by
// task billing, scaled to this site's configured retail base price.
func applyVeo31Pricing(pricing *Pricing, name string, base float64) bool {
	if name == "gemini-omni-1.1-flash" {
		tiers := map[string]float64{"360P": .0337925, "720P": .10136, "1080P": .15204, "4K": .30408}
		common.OptionMapRWMutex.RLock()
		raw := common.OptionMap["VideoModelPricing"]
		common.OptionMapRWMutex.RUnlock()
		var configured map[string]struct {
			Prices map[string]float64 `json:"prices"`
		}
		if common.UnmarshalJsonStr(raw, &configured) == nil {
			for key, value := range configured[name].Prices {
				for variant := range tiers {
					if strings.EqualFold(key, variant) && value > 0 {
						tiers[variant] = value
					}
				}
			}
		}
		if base <= 0 {
			base = tiers["720P"]
		}
		scale := base / tiers["720P"]
		for key, value := range tiers {
			tiers[key] = value * scale
		}
		pricing.ModelPrice, pricing.ResolutionPrices = base, tiers
		pricing.QuotaType, pricing.BillingUnit = 1, "second"
		pricing.SupportedEndpointTypes = []constant.EndpointType{constant.EndpointTypeOpenAIVideo}
		return true
	}

	if name != "veo-3.1-generate-preview" && name != "veo-3.1-fast-generate-preview" && name != "veo-3.1-lite-generate-preview" {
		return false
	}
	tiers := map[string]float64{"720P": .4, "1080P": .4, "4K": .6, "720P-silent": .2, "1080P-silent": .2, "4K-silent": .4}
	if name == "veo-3.1-fast-generate-preview" {
		tiers = map[string]float64{"720P": .1, "1080P": .12, "4K": .3, "720P-silent": .08, "1080P-silent": .1, "4K-silent": .25}
	}
	if name == "veo-3.1-lite-generate-preview" {
		tiers = map[string]float64{"720P": .05, "1080P": .08}
	}
	common.OptionMapRWMutex.RLock()
	raw := common.OptionMap["VideoModelPricing"]
	common.OptionMapRWMutex.RUnlock()
	var configured map[string]struct {
		Prices map[string]float64 `json:"prices"`
	}
	if common.UnmarshalJsonStr(raw, &configured) == nil {
		for key, value := range configured[name].Prices {
			for variant := range tiers {
				if strings.EqualFold(key, variant) && value > 0 {
					tiers[variant] = value
				}
			}
		}
	}
	if base <= 0 {
		base = tiers["720P"]
	}
	scale := base / tiers["720P"]
	for key, value := range tiers {
		tiers[key] = value * scale
	}
	pricing.ModelPrice, pricing.ResolutionPrices = base, tiers
	pricing.QuotaType, pricing.BillingUnit = 1, "second"
	pricing.SupportedEndpointTypes = []constant.EndpointType{constant.EndpointTypeOpenAIVideo}
	return true
}
