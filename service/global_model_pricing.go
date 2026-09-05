package service

import (
	"strings"
	"time"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// PlatformUSDPerModelRatio converts new-api internal model_ratio to USD/1M tokens.
const PlatformUSDPerModelRatio = 2.0

// GlobalModelPricingUSD resolves USD/1M prices from System Settings → Group & Model
// Pricing (ModelPrice / ModelRatio / CompletionRatio / CacheRatio). Tries the
// canonical model name and ModelNameCandidates aliases (e.g. minimax-m3 ↔ MiniMax-M3).
func GlobalModelPricingUSD(canonical string) (input, output, cache, cacheCreation float64, ok bool) {
	return GlobalModelPricingUSDAt(canonical, time.Now())
}

type VideoMediaPricingUSD struct {
	Unit           string
	BasePrice      float64
	BaseVariant    string
	Prices         map[string]float64
	OfficialPrices map[string]float64
}

type ImageMediaPricingUSD struct {
	Unit        string
	BasePrice   float64
	BaseVariant string
	Prices      map[string]float64
}

// ResolvedMediaBasePricingUSD is the user-visible base price for one media
// model on a selected channel. The price is always the same final user price
// that routing and billing would use for that channel.
type ResolvedMediaBasePricingUSD struct {
	Unit          string
	Price         float64
	OfficialPrice float64
}

// GlobalImageMediaPricingUSD resolves the configured per-image price table.
func GlobalImageMediaPricingUSD(canonical string) (ImageMediaPricingUSD, bool) {
	for _, name := range ModelPricingLookupNames(canonical) {
		if pricing, ok := ratio_setting.GetImageModelPricingDetails(name); ok {
			return ImageMediaPricingUSD{
				Unit: pricing.Unit, BasePrice: pricing.BasePrice,
				BaseVariant: pricing.BaseVariant, Prices: pricing.Prices,
			}, true
		}
	}
	return ImageMediaPricingUSD{}, false
}

// GlobalVideoMediaPricingUSD resolves the tiered per-unit price table used by
// both task billing and price presentation. It is the authoritative source for
// media models that have resolution/input variants.
func GlobalVideoMediaPricingUSD(canonical string) (VideoMediaPricingUSD, bool) {
	for _, name := range ModelPricingLookupNames(canonical) {
		if pricing, ok := ratio_setting.GetVideoModelPricingDetails(name); ok {
			return VideoMediaPricingUSD{
				Unit:           pricing.Unit,
				BasePrice:      pricing.BasePrice,
				BaseVariant:    pricing.BaseVariant,
				Prices:         pricing.Prices,
				OfficialPrices: pricing.OfficialPrices,
			}, true
		}
	}
	return VideoMediaPricingUSD{}, false
}

// ResolveChannelMediaBasePricingUSD resolves the base media price presented to
// clients. Models with a dedicated media table retain that table as the source
// of truth so their resolution/mode tiers stay aligned with billing. Older
// video models that are configured only in the standard channel/global price
// catalog fall back to the selected channel's final input price. This is the
// exact price routing would charge, not a fabricated list-price estimate.
func ResolveChannelMediaBasePricingUSD(channelID int, modelName, capability string) (ResolvedMediaBasePricingUSD, bool, error) {
	var (
		basePrice     float64
		officialPrice float64
		unit          string
		hasTable      bool
	)

	switch capability {
	case "image":
		if pricing, ok := GlobalImageMediaPricingUSD(modelName); ok && pricing.BasePrice > 0 {
			basePrice, unit, hasTable = pricing.BasePrice, pricing.Unit, true
			officialPrice = pricing.BasePrice
		}
	case "video":
		if pricing, ok := GlobalVideoMediaPricingUSD(modelName); ok && pricing.BasePrice > 0 {
			basePrice, unit, hasTable = pricing.BasePrice, pricing.Unit, true
			officialPrice = mediaPriceForVariant(pricing.OfficialPrices, pricing.BaseVariant)
		}
	default:
		return ResolvedMediaBasePricingUSD{}, false, nil
	}

	if hasTable {
		price, err := ChannelBaseUserPriceResolved(channelID, modelName, basePrice)
		if err != nil || price <= 0 {
			return ResolvedMediaBasePricingUSD{}, false, err
		}
		if officialPrice <= 0 {
			officialPrice, _, _, _, _ = GlobalModelPricingUSD(modelName)
		}
		return ResolvedMediaBasePricingUSD{Unit: unit, Price: price, OfficialPrice: officialPrice}, true, nil
	}

	// A video model without a tier table is still routable and billable via its
	// normal per-request/per-second model price. Keep that catalog entry visible
	// instead of making clients report "price unavailable".
	if capability != "video" {
		return ResolvedMediaBasePricingUSD{}, false, nil
	}
	resolved, ok, err := ChannelUserPricesResolvedForModel(channelID, modelName)
	if err != nil || !ok || resolved.InputPrice <= 0 {
		return ResolvedMediaBasePricingUSD{}, false, err
	}
	officialPrice, _, _, _, _ = GlobalModelPricingUSD(modelName)
	return ResolvedMediaBasePricingUSD{
		Unit:          "second",
		Price:         resolved.InputPrice,
		OfficialPrice: officialPrice,
	}, true, nil
}

// mediaPriceForVariant follows the marketplace's case-insensitive variant
// lookup. Video pricing tables use the platform billing value as BasePrice,
// while OfficialPrices is the separate crossed-out list price used for savings.
func mediaPriceForVariant(prices map[string]float64, variant string) float64 {
	for name, price := range prices {
		if price > 0 && strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(variant)) {
			return price
		}
	}
	return 0
}

// GlobalModelPricingUSDAt is the time-aware form used by tests and request
// snapshots. DeepSeek V4 has an official peak/off-peak schedule; all other
// models continue to resolve from the operator's static global settings.
func GlobalModelPricingUSDAt(canonical string, at time.Time) (input, output, cache, cacheCreation float64, ok bool) {
	if prices, found := DeepSeekV4OfficialPricingAt(canonical, at); found {
		return prices.InputPrice, prices.OutputPrice, prices.CachePrice, prices.CacheCreationPrice, true
	}
	for _, name := range ModelPricingLookupNames(canonical) {
		// Price-based (quota_type=1: per-request/per-second/per-image, e.g.
		// sora/kling/gpt-image) has no "output token" axis at all — don't run
		// completion_ratio derivation here. GetCompletionRatio falls back to
		// newapi's stock hardcoded per-model-family default when nothing is
		// configured, which fabricates a bogus non-zero "output price" for
		// these models (completion_ratio is a token-billing concept).
		if price, usePrice := ratio_setting.GetModelPrice(name, false); usePrice && price > 0 {
			input = price
			return input, 0, 0, 0, true
		}
		if ratio, success, _ := ratio_setting.GetModelRatio(name); success && ratio > 0 {
			input = ratio * PlatformUSDPerModelRatio
			fillGlobalDerivedPrices(name, input, &output, &cache, &cacheCreation)
			return input, output, cache, cacheCreation, true
		}
	}
	return 0, 0, 0, 0, false
}

func fillGlobalDerivedPrices(name string, input float64, output, cache, cacheCreation *float64) {
	if input <= 0 {
		return
	}
	if comp := ratio_setting.GetCompletionRatio(name); comp > 0 {
		*output = input * comp
	}
	if cr, crOk := ratio_setting.GetCacheRatio(name); crOk && cr > 0 {
		*cache = input * cr
	}
	if cc, ccOk := ratio_setting.GetCreateCacheRatio(name); ccOk && cc > 0 {
		*cacheCreation = input * cc
	}
}

// modelPricingLookupNames expands canonical ids and provider aliases (MiniMax-M3 ↔ minimax-m3).
func ModelPricingLookupNames(name string) []string {
	out := appendUniqueStrings(ModelNameCandidates(name), name)
	for canonical, aliases := range ModelIDCandidates {
		for _, alias := range aliases {
			if strings.EqualFold(alias, name) {
				out = appendUniqueStrings(out, canonical, alias)
			}
		}
	}
	return out
}
