package service

import (
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

type ConsumeAccountingInput struct {
	ProcurementTieredSnapshot *billingexpr.BillingSnapshot
	WalletTieredSnapshot      *billingexpr.BillingSnapshot
	TieredRequestInput        *billingexpr.RequestInput
	TieredUsage               *dto.Usage
	UserId                    int
	ChannelId                 int
	ModelName                 string
	InputTokens               int
	InputTokensIncludeCache   bool
	CacheTokenSemanticSource  string
	OutputTokens              int
	CacheReadTokens           int
	CacheWriteTokens          int
	CacheWriteTokens5m        int
	CacheWriteTokens1h        int
	CacheWritePriceMultiplier *float64
	BillingMode               string
	ImageCount                int
	ImageBaseUnits            float64
	ImagePriceVariant         string
	DurationUnitPrice         float64
	DurationPriceVariant      string
	DurationSeconds           int
	GroupRatio                float64
	UserModelDiscount         float64
	Quota                     int
	ZeroUserCharge            bool
	UseQuotaForUserAmounts    bool
	UnpricedToolSurchargeUSD  float64
	BillingAt                 time.Time
}

type accountingPriceTuple struct {
	InputPrice           float64  `json:"input_price"`
	OutputPrice          float64  `json:"output_price"`
	CachePrice           float64  `json:"cache_price"`
	CacheCreationPrice   float64  `json:"cache_creation_price"`
	CacheCreationPrice1h *float64 `json:"cache_creation_price_1h"`
}

type consumeAccountingSnapshot struct {
	Version                  int                `json:"version"`
	ImageBaseUnits           float64            `json:"image_base_units,omitempty"`
	ImagePriceVariant        string             `json:"image_price_variant,omitempty"`
	Currency                 string             `json:"currency"`
	Status                   string             `json:"status"`
	Error                    string             `json:"error,omitempty"`
	UserId                   int                `json:"user_id"`
	ChannelId                int                `json:"channel_id"`
	ModelName                string             `json:"model_name"`
	ResellerUserId           int                `json:"reseller_user_id,omitempty"`
	ResellerRuleId           int                `json:"reseller_rule_id,omitempty"`
	ResellerDiscountRatio    float64            `json:"reseller_discount_ratio,omitempty"`
	GroupRatio               float64            `json:"group_ratio"`
	UserModelDiscount        float64            `json:"user_model_discount,omitempty"`
	Quota                    int                `json:"quota"`
	BillingMode              string             `json:"billing_mode,omitempty"`
	InputTokensIncludeCache  bool               `json:"input_tokens_include_cache"`
	CacheTokenSemanticSource string             `json:"cache_token_semantic_source,omitempty"`
	Tokens                   map[string]int     `json:"tokens"`
	Units                    map[string]int     `json:"units,omitempty"`
	Prices                   map[string]any     `json:"prices"`
	AmountsUSD               map[string]float64 `json:"amounts_usd"`
	AccountingAmountVersion  string             `json:"accounting_amount_version"`
	DurationPriceVariant     string             `json:"duration_price_variant,omitempty"`
	DurationPriceRatio       float64            `json:"duration_price_ratio,omitempty"`
	CostPriceSource          string             `json:"cost_price_source,omitempty"`
}

const (
	accountingBillingModeToken           = "token"
	accountingBillingModeImageCount      = "image_count"
	accountingBillingModeDurationSeconds = "duration_seconds"
)

func BuildConsumeAccountingFields(input ConsumeAccountingInput) (fields model.AccountingLogFields) {
	defer func() {
		if r := recover(); r != nil {
			fields.Status = "error"
			fields.Snapshot = common.MapToJsonStr(map[string]interface{}{
				"version":  2,
				"currency": "USD",
				"status":   "error",
				"error":    fmt.Sprintf("panic: %v", r),
			})
		}
	}()

	if input.GroupRatio <= 0 {
		input.GroupRatio = 1
	}
	if input.UserModelDiscount > 0 && input.UserModelDiscount < 1 {
		input.UseQuotaForUserAmounts = true
	}
	// Match image relay pricing: the configured media base price is authoritative,
	// including resolution weights, even if upstream token-price rows are absent.
	imagePricing, useImagePricing := GlobalImageMediaPricingUSD(input.ModelName)
	useImagePricing = useImagePricing && normalizedAccountingBillingMode(input) == accountingBillingModeImageCount
	if useImagePricing && input.ImageBaseUnits <= 0 {
		input.ImageBaseUnits = float64(input.ImageCount) * ratio_setting.GetImageModelPriceRatio(input.ModelName, input.ImagePriceVariant)
	}
	snap := consumeAccountingSnapshot{
		Version:                  2,
		ImageBaseUnits:           input.ImageBaseUnits,
		ImagePriceVariant:        input.ImagePriceVariant,
		Currency:                 "USD",
		Status:                   "ok",
		UserId:                   input.UserId,
		ChannelId:                input.ChannelId,
		ModelName:                input.ModelName,
		GroupRatio:               input.GroupRatio,
		UserModelDiscount:        input.UserModelDiscount,
		Quota:                    input.Quota,
		BillingMode:              normalizedAccountingBillingMode(input),
		InputTokensIncludeCache:  input.InputTokensIncludeCache,
		CacheTokenSemanticSource: input.CacheTokenSemanticSource,
		AccountingAmountVersion:  "mixed_billing_v1",
		Tokens: map[string]int{
			"input":          input.InputTokens,
			"output":         input.OutputTokens,
			"cache_read":     input.CacheReadTokens,
			"cache_write":    normalizedAccountingCacheWriteTotal(input),
			"cache_write_5m": max(0, normalizedAccountingCacheWriteTotal(input)-max(0, input.CacheWriteTokens1h)),
			"cache_write_1h": max(0, input.CacheWriteTokens1h),
		},
		Prices:     map[string]any{},
		AmountsUSD: map[string]float64{},
	}
	if input.CacheWritePriceMultiplier != nil {
		snap.AccountingAmountVersion = "mixed_billing_v2_cache_ttl"
		snap.Tokens["cache_write_5m"] = input.CacheWriteTokens5m
		snap.Tokens["cache_write_1h"] = input.CacheWriteTokens1h
		snap.Prices["cache_write_price_multiplier"] = *input.CacheWritePriceMultiplier
	}
	if input.CacheWriteTokens1h > 0 {
		snap.AccountingAmountVersion = "mixed_billing_v3_cache_ttl_prices"
	}
	if units := accountingUnits(input); len(units) > 0 {
		snap.Units = units
	}

	status := "ok"
	errorText := ""

	var channelCost *model.ChannelActualPrices
	var err error
	if useImagePricing {
		ch, lookupErr := loadChannelPricingResolveContext(input.ChannelId)
		err = lookupErr
		if err == nil {
			cost := imagePricing.BasePrice * channelBasePriceGroupRatio(input.ChannelId, input.ModelName, ch) * ch.RechargeRate
			channelCost = &model.ChannelActualPrices{InputPrice: cost}
		}
	} else if prices, ok := DeepSeekV4ProcurementPricingAt(input.ChannelId, input.ModelName, input.BillingAt); ok {
		channelCost = &model.ChannelActualPrices{
			InputPrice:         prices.InputPrice,
			OutputPrice:        prices.OutputPrice,
			CachePrice:         prices.CachePrice,
			CacheCreationPrice: prices.CacheCreationPrice,
		}
	} else {
		channelCost, err = ChannelProcurementPricesResolved(input.ChannelId, input.ModelName)
	}
	if err != nil {
		status = "partial"
		errorText = appendAccountingError(errorText, "channel_cost_lookup_failed: "+err.Error())
	} else if channelCost == nil {
		status = "partial"
		errorText = appendAccountingError(errorText, "channel_cost_price_missing")
	} else {
		tuple := tupleFromChannelPrices(channelCost)
		tuple.CacheCreationPrice1h = ratio_setting.ResolveCacheCreation1hPrice(input.ModelName, tuple.CacheCreationPrice, tuple.CacheCreationPrice1h)
		if input.DurationPriceVariant != "" && (IsSeedanceLibraryModel(input.ModelName) || IsVeo31VideoModel(input.ModelName) || input.ModelName == "kling-v4") && normalizedAccountingBillingMode(input) == accountingBillingModeDurationSeconds {
			ratio := ratio_setting.GetVideoModelOfficialPriceRatio(input.ModelName, input.DurationPriceVariant)
			if ratio <= 0 {
				status = "partial"
				errorText = appendAccountingError(errorText, "video_procurement_variant_missing")
			} else {
				tuple.InputPrice *= ratio
				snap.DurationPriceVariant = input.DurationPriceVariant
				snap.DurationPriceRatio = ratio
				snap.CostPriceSource = "configured_video_variant_estimate"
			}
		}
		snap.Prices["channel_cost"] = tuple
		fields.ChannelCostAmountUSD = amountUSD(tuple, input)
		snap.AmountsUSD["channel_cost"] = fields.ChannelCostAmountUSD
	}

	var userPrice *model.ChannelActualPrices
	if input.DurationUnitPrice > 0 && normalizedAccountingBillingMode(input) == accountingBillingModeDurationSeconds {
		userPrice = &model.ChannelActualPrices{InputPrice: input.DurationUnitPrice}
		err = nil
	} else if useImagePricing {
		price, lookupErr := ChannelBaseUserPriceResolved(input.ChannelId, input.ModelName, imagePricing.BasePrice)
		err = lookupErr
		if err == nil {
			userPrice = &model.ChannelActualPrices{InputPrice: price}
		}
	} else if prices, ok := DeepSeekV4UserPricingAt(input.ChannelId, input.ModelName, input.BillingAt); ok {
		userPrice = &model.ChannelActualPrices{
			InputPrice:         prices.InputPrice,
			OutputPrice:        prices.OutputPrice,
			CachePrice:         prices.CachePrice,
			CacheCreationPrice: prices.CacheCreationPrice,
		}
	} else {
		userPrice, err = ChannelActualPricesResolved(input.ChannelId, input.ModelName)
	}
	if err != nil {
		status = "partial"
		errorText = appendAccountingError(errorText, "user_price_lookup_failed: "+err.Error())
	} else if userPrice == nil {
		status = "partial"
		errorText = appendAccountingError(errorText, "user_price_missing")
	} else {
		tuple := tupleFromChannelPrices(userPrice)
		snap.Prices["user_price"] = tuple
		fields.UserPriceAmountUSD, fields.UserFinalAmountUSD = userAmountsUSD(tuple, input)
		if input.UseQuotaForUserAmounts {
			snap.Prices["user_amount_source"] = "settled_quota"
		}
		finalPrice := accountingPriceTuple{
			InputPrice:           tuple.InputPrice * input.GroupRatio,
			OutputPrice:          tuple.OutputPrice * input.GroupRatio,
			CachePrice:           tuple.CachePrice * input.GroupRatio,
			CacheCreationPrice:   tuple.CacheCreationPrice * input.GroupRatio,
			CacheCreationPrice1h: multiplyOptionalPrice(tuple.CacheCreationPrice1h, input.GroupRatio),
		}
		if input.ZeroUserCharge {
			finalPrice = accountingPriceTuple{}
		}
		snap.Prices["user_final_price"] = finalPrice
		snap.AmountsUSD["user_price"] = fields.UserPriceAmountUSD
		snap.AmountsUSD["user_final"] = fields.UserFinalAmountUSD
	}

	official := accountingPriceTuple{}
	if input.ModelName != "" {
		in, out, cache, cacheCreation, ok := GlobalModelPricingUSDAt(input.ModelName, input.BillingAt)
		if useImagePricing {
			in, out, cache, cacheCreation, ok = imagePricing.BasePrice, 0, 0, 0, true
		}
		if ok {
			official = accountingPriceTuple{
				InputPrice:           in,
				OutputPrice:          out,
				CachePrice:           cache,
				CacheCreationPrice:   cacheCreation,
				CacheCreationPrice1h: ratio_setting.ResolveCacheCreation1hPrice(input.ModelName, cacheCreation, nil),
			}
			snap.Prices["official_price"] = official
		} else {
			status = "partial"
			errorText = appendAccountingError(errorText, "official_price_missing")
		}
	}

	var user model.User
	if err := model.DB.Select("id, reseller_user_id").Where("id = ?", input.UserId).First(&user).Error; err != nil {
		status = "partial"
		errorText = appendAccountingError(errorText, "user_lookup_failed: "+err.Error())
	} else if user.ResellerUserId > 0 {
		fields.ResellerUserId = user.ResellerUserId
		snap.ResellerUserId = user.ResellerUserId
		rule, err := getEnabledResellerRuleByAnyModelName(user.ResellerUserId, input.UserId, input.ModelName)
		if err != nil {
			status = "partial"
			errorText = appendAccountingError(errorText, "reseller_rule_lookup_failed: "+err.Error())
		} else if rule == nil {
			status = "missing_reseller_rule"
			errorText = appendAccountingError(errorText, "reseller_rule_missing")
		} else {
			fields.ResellerRuleId = rule.Id
			fields.ResellerDiscountRatio = rule.DiscountRatio
			snap.ResellerRuleId = rule.Id
			snap.ResellerDiscountRatio = rule.DiscountRatio
			if _, ok := snap.Prices["official_price"]; ok {
				resellerCost := accountingPriceTuple{
					InputPrice:           official.InputPrice * rule.DiscountRatio,
					OutputPrice:          official.OutputPrice * rule.DiscountRatio,
					CachePrice:           official.CachePrice * rule.DiscountRatio,
					CacheCreationPrice:   official.CacheCreationPrice * rule.DiscountRatio,
					CacheCreationPrice1h: multiplyOptionalPrice(official.CacheCreationPrice1h, rule.DiscountRatio),
				}
				snap.Prices["reseller_cost"] = resellerCost
				fields.ResellerCostAmountUSD = amountUSD(resellerCost, input)
				snap.AmountsUSD["reseller_cost"] = fields.ResellerCostAmountUSD
			}
		}
	}

	if err := applyTieredAccountingFields(input, &fields, &snap); err != nil {
		status = "partial"
		errorText = appendAccountingError(errorText, "tiered_accounting_failed: "+err.Error())
	}
	if input.UnpricedToolSurchargeUSD > 0 {
		// Token procurement prices do not define the supplier's tool-call fee.
		// Keep the known token subtotal and explicitly mark that missing cost;
		// never report it as a verified zero or silently omit the user charge.
		if status == "ok" {
			status = "partial"
		}
		errorText = appendAccountingError(errorText, "tool_procurement_price_unconfigured")
		snap.Prices["tool_surcharge"] = map[string]any{"charged_usd": input.UnpricedToolSurchargeUSD, "procurement_status": "unconfigured", "channel_cost_includes_tool_fee": false}
		if !input.ZeroUserCharge {
			fields.UserFinalAmountUSD = quotaAmountUSD(input.Quota)
			fields.UserPriceAmountUSD = fields.UserFinalAmountUSD / input.GroupRatio
			snap.AmountsUSD["user_final"] = fields.UserFinalAmountUSD
			snap.AmountsUSD["user_price"] = fields.UserPriceAmountUSD
			snap.Prices["user_amount_source"] = "settled_quota"
		}
	}
	if input.UserModelDiscount > 0 && input.UserModelDiscount < 1 && !input.ZeroUserCharge {
		fields.UserFinalAmountUSD = quotaAmountUSD(input.Quota)
		fields.UserPriceAmountUSD = fields.UserFinalAmountUSD / input.GroupRatio
		snap.AmountsUSD["user_final"] = fields.UserFinalAmountUSD
		snap.AmountsUSD["user_price"] = fields.UserPriceAmountUSD
		snap.Prices["user_amount_source"] = "settled_quota"
	}
	if input.CacheWriteTokens1h > 0 {
		if tuple, ok := snap.Prices["channel_cost"].(accountingPriceTuple); ok && tuple.CacheCreationPrice1h == nil {
			status = "partial"
			errorText = appendAccountingError(errorText, "channel_cache_1h_price_missing")
		}
	}
	fields.GroupRatio = input.GroupRatio
	fields.Status = status
	if errorText != "" {
		snap.Error = errorText
	}
	snap.Status = status
	fields.Snapshot = common.GetJsonString(snap)
	return fields
}

func tupleFromChannelPrices(p *model.ChannelActualPrices) accountingPriceTuple {
	if p == nil {
		return accountingPriceTuple{}
	}
	return accountingPriceTuple{
		InputPrice:           p.InputPrice,
		OutputPrice:          p.OutputPrice,
		CachePrice:           p.CachePrice,
		CacheCreationPrice:   p.CacheCreationPrice,
		CacheCreationPrice1h: p.CacheCreationPrice1h,
	}
}

func amountUSD(prices accountingPriceTuple, input ConsumeAccountingInput) float64 {
	switch normalizedAccountingBillingMode(input) {
	case accountingBillingModeImageCount:
		if input.ImageBaseUnits > 0 {
			return prices.InputPrice * input.ImageBaseUnits
		}
		if input.ImageCount > 0 {
			return prices.InputPrice * float64(input.ImageCount)
		}
	case accountingBillingModeDurationSeconds:
		if input.DurationSeconds > 0 {
			return prices.InputPrice * float64(input.DurationSeconds)
		}
	}

	inputTokens := input.InputTokens
	if input.InputTokensIncludeCache {
		inputTokens -= input.CacheReadTokens + normalizedAccountingCacheWriteTotal(input)
		if inputTokens < 0 {
			inputTokens = 0
		}
	}
	cacheWriteAmount := cacheWriteCostUSD(prices, input)
	// Legacy callers with no split still carry their existing multiplier.
	if input.CacheWriteTokens1h <= 0 && input.CacheWriteTokens5m <= 0 && input.CacheWritePriceMultiplier != nil {
		cacheWriteAmount *= *input.CacheWritePriceMultiplier
	}
	return (prices.InputPrice*float64(inputTokens) +
		prices.OutputPrice*float64(input.OutputTokens) +
		prices.CachePrice*float64(input.CacheReadTokens) +
		cacheWriteAmount) / 1000000.0
}

func userAmountsUSD(prices accountingPriceTuple, input ConsumeAccountingInput) (userPriceAmountUSD float64, userFinalAmountUSD float64) {
	if input.ZeroUserCharge {
		return 0, 0
	}
	if input.UseQuotaForUserAmounts {
		userFinalAmountUSD = quotaAmountUSD(input.Quota)
		if input.GroupRatio <= 0 {
			return userFinalAmountUSD, userFinalAmountUSD
		}
		return userFinalAmountUSD / input.GroupRatio, userFinalAmountUSD
	}
	switch normalizedAccountingBillingMode(input) {
	case accountingBillingModeImageCount, accountingBillingModeDurationSeconds:
		userFinalAmountUSD = quotaAmountUSD(input.Quota)
		if input.GroupRatio <= 0 {
			return userFinalAmountUSD, userFinalAmountUSD
		}
		return userFinalAmountUSD / input.GroupRatio, userFinalAmountUSD
	default:
		userPriceAmountUSD = amountUSD(prices, input)
		return userPriceAmountUSD, userPriceAmountUSD * input.GroupRatio
	}
}

func quotaAmountUSD(quota int) float64 {
	return float64(quota) / common.QuotaPerUnit
}

func normalizedAccountingBillingMode(input ConsumeAccountingInput) string {
	switch input.BillingMode {
	case accountingBillingModeImageCount:
		if input.ImageCount > 0 {
			return accountingBillingModeImageCount
		}
	case accountingBillingModeDurationSeconds:
		if input.DurationSeconds > 0 {
			return accountingBillingModeDurationSeconds
		}
	}
	if input.ImageCount > 0 {
		return accountingBillingModeImageCount
	}
	if input.DurationSeconds > 0 {
		return accountingBillingModeDurationSeconds
	}
	return accountingBillingModeToken
}

func accountingUnits(input ConsumeAccountingInput) map[string]int {
	switch normalizedAccountingBillingMode(input) {
	case accountingBillingModeImageCount:
		return map[string]int{"image_count": input.ImageCount}
	case accountingBillingModeDurationSeconds:
		return map[string]int{"duration_seconds": input.DurationSeconds}
	default:
		return nil
	}
}

func appendAccountingError(existing string, msg string) string {
	if existing == "" {
		return msg
	}
	return existing + "; " + msg
}

func getEnabledResellerRuleByAnyModelName(resellerId int, downlineId int, modelName string) (*model.ResellerModelRule, error) {
	names := ModelPricingLookupNames(modelName)
	for _, name := range appendUniqueStrings(names, modelName) {
		rule, err := model.GetEnabledResellerRule(resellerId, downlineId, name)
		if err != nil || rule != nil {
			return rule, err
		}
	}
	return nil, nil
}

func normalizedAccountingCacheWriteTotal(input ConsumeAccountingInput) int {
	return max(0, max(input.CacheWriteTokens, max(0, input.CacheWriteTokens5m)+max(0, input.CacheWriteTokens1h)))
}
func cacheWriteCostUSD(prices accountingPriceTuple, input ConsumeAccountingInput) float64 {
	total := normalizedAccountingCacheWriteTotal(input)
	oneHour := max(0, input.CacheWriteTokens1h)
	price1h := ratio_setting.ResolveCacheCreation1hPrice(input.ModelName, prices.CacheCreationPrice, prices.CacheCreationPrice1h)
	if price1h == nil {
		if oneHour > 0 && input.CacheWritePriceMultiplier != nil {
			return prices.CacheCreationPrice * float64(total) * *input.CacheWritePriceMultiplier
		}
	}
	cost := prices.CacheCreationPrice * float64(total-oneHour)
	if price1h != nil {
		cost += *price1h * float64(oneHour)
	}
	return cost
}
func multiplyOptionalPrice(price *float64, ratio float64) *float64 {
	if price == nil {
		return nil
	}
	value := *price * ratio
	return &value
}
