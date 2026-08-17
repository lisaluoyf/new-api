package service

import "time"

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
// Peak hours are 09:00-12:00 and 14:00-18:00 in Beijing time. The caller is
// expected to pass the request start time so a long request cannot change
// price while it is in flight. Prices verified 2026-08-17 against:
// https://api-docs.deepseek.com/quick_start/pricing/
func DeepSeekV4OfficialPricingAt(modelName string, at time.Time) (ModelUnitPricesUSD, bool) {
	if at.IsZero() {
		at = time.Now()
	}
	hour := at.In(deepSeekV4Timezone).Hour()
	peak := (hour >= 9 && hour < 12) || (hour >= 14 && hour < 18)

	var offPeak ModelUnitPricesUSD
	switch modelName {
	case "deepseek-v4-flash":
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
	if !peak {
		return offPeak, true
	}
	return scaleModelUnitPrices(offPeak, 2), true
}

// DeepSeekV4UserPricingAt applies the existing per-model/channel user-price
// multiplier to the official time-of-day base price. Procurement price and
// recharge_rate intentionally do not participate in the user selling price.
func DeepSeekV4UserPricingAt(channelID int, modelName string, at time.Time) (ModelUnitPricesUSD, bool) {
	prices, ok := DeepSeekV4OfficialPricingAt(modelName, at)
	if !ok {
		return ModelUnitPricesUSD{}, false
	}
	return scaleModelUnitPrices(prices, ChannelUserPriceRatio(channelID, modelName)), true
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
