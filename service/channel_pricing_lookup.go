package service

import "github.com/QuantumNous/new-api/model"

type ChannelModelPriceRatios struct {
	ModelRatio         float64
	CompletionRatio    float64
	CacheRatio         float64
	CacheCreationRatio float64
}

// ChannelUserPricesResolved contains the final user-facing prices for a
// channel/model pair. Values are USD per 1M tokens for text models; callers
// may use the same channel multipliers with media base prices via
// ChannelBaseUserPriceResolved.
type ChannelUserPricesResolved struct {
	InputPrice         float64
	OutputPrice        float64
	CachePrice         float64
	CacheCreationPrice float64
	GroupRatio         float64
	RechargeRate       float64
	PriceRatio         float64
}

// ChannelUserPricesResolvedForModel resolves the complete text price tuple
// using the same model mapping/manual/global fallbacks as request billing.
func ChannelUserPricesResolvedForModel(channelID int, modelName string) (ChannelUserPricesResolved, bool, error) {
	ch, err := loadChannelPricingResolveContext(channelID)
	if err != nil {
		return ChannelUserPricesResolved{}, false, err
	}
	row, ok := resolveChannelPricingRow(channelID, modelName, ch)
	applyGroupRatio := false
	if !ok {
		input, output, cache, cacheCreation, globalOK := GlobalModelPricingUSD(modelName)
		if !globalOK || input <= 0 {
			return ChannelUserPricesResolved{}, false, nil
		}
		row = &ChannelPricingLookupRow{
			InputPrice: input, OutputPrice: output,
			CachePrice: cache, CacheCreationPrice: cacheCreation,
			GroupRatio: channelBasePriceGroupRatio(channelID, modelName, ch),
		}
		applyGroupRatio = true
	}
	groupRatio := 1.0
	if applyGroupRatio {
		groupRatio = channelBasePriceGroupRatio(channelID, modelName, ch)
		if groupRatio <= 0 {
			groupRatio = 1
		}
	}
	priceRatio := ch.EffectivePriceRatio(modelName)
	if priceRatio <= 0 {
		priceRatio = 1
	}
	mult := groupRatio * ch.RechargeRate * priceRatio
	return ChannelUserPricesResolved{
		InputPrice: row.InputPrice * mult, OutputPrice: row.OutputPrice * mult,
		CachePrice: row.CachePrice * mult, CacheCreationPrice: row.CacheCreationPrice * mult,
		GroupRatio: groupRatio, RechargeRate: ch.RechargeRate, PriceRatio: priceRatio,
	}, true, nil
}

// EffectiveModelPriceRatio resolves the user-price markup for a (channel, model)
// pair: per-model override (tried across model name aliases) > channel-level
// ratio > 1.0. Alias matching mirrors pricing-row lookup so an override keyed
// "claude-haiku-4-5" also applies to dated variants.
func EffectiveModelPriceRatio(modelPriceRatiosJSON *string, channelRatio *float64, modelName string) float64 {
	for _, name := range ModelPricingLookupNames(modelName) {
		if r, ok := model.LookupModelPriceRatio(modelPriceRatiosJSON, name); ok {
			return r
		}
	}
	if channelRatio == nil || *channelRatio <= 0 {
		return 1.0
	}
	return *channelRatio
}

// ChannelUserPriceRatio returns the existing user selling-price multiplier for
// a channel/model. It deliberately excludes recharge_rate: that field belongs
// to procurement cost, while this multiplier is the user-price policy.
func ChannelUserPriceRatio(channelID int, modelName string) float64 {
	if channelID <= 0 || model.DB == nil {
		return 1
	}
	var ch struct {
		ApimasterPriceRatio float64
		ModelPriceRatios    *string
	}
	if err := model.DB.Table("channels").
		Select("COALESCE(apimaster_price_ratio, 1.0) AS apimaster_price_ratio, model_price_ratios").
		Where("id = ?", channelID).
		Scan(&ch).Error; err != nil {
		return 1
	}
	return EffectiveModelPriceRatio(ch.ModelPriceRatios, &ch.ApimasterPriceRatio, modelName)
}

// ChannelModelPriceRatio derives newapi-internal ratio numbers from a specific
// channel's row in channel_model_pricings. Returns (0, 0, false) when no
// pricing row exists for the (channel, model) pair.
//
// Conversion (newapi internal scale: 1.0 ratio == $2/1M tokens, baked into
// setting/ratio_setting/model_ratio.go):
//
//	model_ratio       = input_price / 2.0
//	completion_ratio  = output_price / input_price   (defaults to 1.0 when output_price missing)
//
// Used by relay/helper/price.go ModelPriceHelper as a fallback when neither
// ModelPrice nor ModelRatio is configured — implements apimaster's
// "cost price == sell price" routing 0.1 + step 4 billing model.
func ChannelModelPriceData(channelID int, modelName string) (ChannelModelPriceRatios, bool) {
	var ch struct {
		ModelMapping        *string
		Setting             *string
		RechargeRate        float64
		ApimasterPriceRatio float64
		ModelPriceRatios    *string
	}
	_ = model.DB.Table("channels").
		Select("model_mapping, setting, COALESCE(recharge_rate, 1.0) AS recharge_rate, COALESCE(apimaster_price_ratio, 1.0) AS apimaster_price_ratio, model_price_ratios").
		Where("id = ?", channelID).
		Scan(&ch).Error

	pricing, ok := LookupPreferredChannelPricingRow(channelID, modelName, ch.ModelMapping)
	var inputPrice, outputPrice, cachePrice, cacheCreationPrice float64
	if ok {
		inputPrice, outputPrice, cachePrice, cacheCreationPrice =
			pricing.InputPrice, pricing.OutputPrice, pricing.CachePrice, pricing.CacheCreationPrice
	} else {
		// No stored row — resolve live for manual-priced channels (see
		// fetchModelPriceRatioFallback: manual channels never store a
		// snapshot, precisely so a 官方原价 edit takes effect on the very
		// next request instead of waiting for "刷新价格").
		manual, manualOk := LookupPublicManualPricing(ch.Setting, modelName)
		if !manualOk || manual.InputPrice <= 0 {
			return ChannelModelPriceRatios{}, false
		}
		inputPrice, outputPrice, cachePrice, cacheCreationPrice =
			manual.InputPrice, manual.OutputPrice, manual.CachePrice, manual.CacheCreationPrice
	}
	row := struct {
		InputPrice         float64
		OutputPrice        float64
		CachePrice         float64
		CacheCreationPrice float64
		RechargeRate       float64
	}{
		InputPrice:         inputPrice,
		OutputPrice:        outputPrice,
		CachePrice:         cachePrice,
		CacheCreationPrice: cacheCreationPrice,
		RechargeRate:       ch.RechargeRate,
	}
	if row.RechargeRate <= 0 {
		row.RechargeRate = 1.0
	}
	rechargeRate := row.RechargeRate
	if rechargeRate <= 0 {
		rechargeRate = 1.0
	}
	// apimaster markup multiplier (per-model override > channel default > 1.0).
	// Applied to ModelRatio (input). Output/cache ride along automatically because
	// their ratios are relative to input_price.
	apimasterRatio := EffectiveModelPriceRatio(ch.ModelPriceRatios, &ch.ApimasterPriceRatio, modelName)
	priceData := ChannelModelPriceRatios{
		ModelRatio:      row.InputPrice * rechargeRate * apimasterRatio / 2.0,
		CompletionRatio: 1.0,
	}
	if row.OutputPrice > 0 {
		priceData.CompletionRatio = row.OutputPrice / row.InputPrice
	}
	if row.CachePrice > 0 {
		priceData.CacheRatio = row.CachePrice / row.InputPrice
	}
	if row.CacheCreationPrice > 0 {
		priceData.CacheCreationRatio = row.CacheCreationPrice / row.InputPrice
	}
	return priceData, true
}

func ChannelModelPriceRatio(channelID int, modelName string) (modelRatio, completionRatio float64, ok bool) {
	priceData, ok := ChannelModelPriceData(channelID, modelName)
	if !ok {
		return 0, 0, false
	}
	return priceData.ModelRatio, priceData.CompletionRatio, true
}
