package service

import (
	"strings"
	"time"

	"github.com/QuantumNous/new-api/model"
)

const deepSeekV4TimezoneOffsetSeconds = 8 * 60 * 60

var deepSeekV4Timezone = time.FixedZone("Asia/Shanghai", deepSeekV4TimezoneOffsetSeconds)

// ModelUnitPricesUSD is a model's token price in USD per 1M tokens.
type ModelUnitPricesUSD struct {
	InputPrice         float64
	OutputPrice        float64
	CachePrice         float64
	CacheCreationPrice float64
}

// DeepSeekV4OfficialPricingAt returns DeepSeek's official time-of-day price.
// Peak hours are 09:00-12:00 and 14:00-18:00 in Beijing time, Monday-Friday.
// The caller is expected to pass the request start time so a long request
// cannot change price while it is in flight. Prices verified against:
// https://api-docs.deepseek.com/quick_start/pricing/
func DeepSeekV4OfficialPricingAt(modelName string, at time.Time) (ModelUnitPricesUSD, bool) {
	if at.IsZero() {
		at = time.Now()
	}
	period, ok := DeepSeekV4PricingPeriodAt(modelName, at)
	if !ok {
		return ModelUnitPricesUSD{}, false
	}

	canonicalModel, ok := deepSeekV4PricingModel(modelName)
	if !ok {
		return ModelUnitPricesUSD{}, false
	}

	var offPeak ModelUnitPricesUSD
	switch canonicalModel {
	case "deepseek-v4-flash", "deepseek-v4-flash-vision-exp":
		offPeak = ModelUnitPricesUSD{
			InputPrice:         0.22,
			OutputPrice:        0.66,
			CachePrice:         0.007,
			CacheCreationPrice: 0.22,
		}
	case "deepseek-v4-pro":
		offPeak = ModelUnitPricesUSD{
			InputPrice:         0.66,
			OutputPrice:        1.98,
			CachePrice:         0.022,
			CacheCreationPrice: 0.66,
		}
	default:
		return ModelUnitPricesUSD{}, false
	}
	if period == "off_peak" {
		return offPeak, true
	}
	return scaleModelUnitPrices(offPeak, 2), true
}

// DeepSeekV4PricingPeriodAt returns the official pricing period in Beijing time.
func DeepSeekV4PricingPeriodAt(modelName string, at time.Time) (string, bool) {
	if _, ok := deepSeekV4PricingModel(modelName); !ok {
		return "", false
	}
	if at.IsZero() {
		at = time.Now()
	}
	beijingTime := at.In(deepSeekV4Timezone)
	if beijingTime.Weekday() == time.Saturday || beijingTime.Weekday() == time.Sunday {
		return "off_peak", true
	}
	hour := beijingTime.Hour()
	if (hour >= 9 && hour < 12) || (hour >= 14 && hour < 18) {
		return "peak", true
	}
	return "off_peak", true
}

func deepSeekV4PricingModel(modelName string) (string, bool) {
	canonical := modelName
	for _, suffix := range []string{"-none", "-max"} {
		if strings.HasSuffix(canonical, suffix) {
			canonical = strings.TrimSuffix(canonical, suffix)
			break
		}
	}
	switch canonical {
	case "deepseek-v4-flash", "deepseek-v4-pro", "deepseek-v4-flash-vision-exp":
		return canonical, true
	default:
		return "", false
	}
}

// DeepSeekV4ProcurementPricingAt treats the official time-of-day price as the
// channel's base price, then applies the channel's upstream group multiplier
// and recharge rate using the existing procurement-cost policy.
func DeepSeekV4ProcurementPricingAt(channelID int, modelName string, at time.Time) (ModelUnitPricesUSD, bool) {
	prices, ok := DeepSeekV4OfficialPricingAt(modelName, at)
	if !ok {
		return ModelUnitPricesUSD{}, false
	}
	groupRatio, rechargeRate, _ := deepSeekV4ChannelMultipliers(channelID, modelName)
	return scaleModelUnitPrices(prices, groupRatio*rechargeRate), true
}

// DeepSeekV4UserPricingAt preserves the existing channel selling-price chain:
// official time-of-day base × upstream group × recharge_rate × user multiplier.
func DeepSeekV4UserPricingAt(channelID int, modelName string, at time.Time) (ModelUnitPricesUSD, bool) {
	prices, ok := DeepSeekV4OfficialPricingAt(modelName, at)
	if !ok {
		return ModelUnitPricesUSD{}, false
	}
	groupRatio, rechargeRate, userPriceRatio := deepSeekV4ChannelMultipliers(channelID, modelName)
	return scaleModelUnitPrices(prices, groupRatio*rechargeRate*userPriceRatio), true
}

func deepSeekV4ChannelMultipliers(channelID int, modelName string) (groupRatio, rechargeRate, userPriceRatio float64) {
	groupRatio, rechargeRate, userPriceRatio = 1, 1, 1
	if channelID <= 0 || model.DB == nil {
		return
	}

	ch, err := loadChannelPricingResolveContext(channelID)
	if err != nil {
		return
	}
	rechargeRate = ch.RechargeRate
	userPriceRatio = ch.EffectivePriceRatio(modelName)

	if manualGroupRatio := ExtractManualGroupRatio(ch.Setting); manualGroupRatio > 0 {
		groupRatio = manualGroupRatio
	} else if row, ok := LookupPreferredChannelPricingRow(channelID, modelName, ch.ModelMapping); ok {
		groupRatio = row.GroupRatio
	}
	return
}

func scaleModelUnitPrices(prices ModelUnitPricesUSD, ratio float64) ModelUnitPricesUSD {
	if ratio <= 0 {
		ratio = 1
	}
	prices.InputPrice *= ratio
	prices.OutputPrice *= ratio
	prices.CachePrice *= ratio
	prices.CacheCreationPrice *= ratio
	return prices
}
