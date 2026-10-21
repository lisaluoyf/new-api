package service

import (
	"math"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// cacheReadUserDiscountScale cancels only the personal discount on cache reads
// for the exact public model/channel pair. Channel prices, time tiers and group
// pricing still apply. Use the frozen applied discount, never current user settings.
func cacheReadUserDiscountScale(info *relaycommon.RelayInfo) float64 {
	if info == nil || info.ChannelMeta == nil || info.ChannelId != 204 || info.OriginModelName != "deepseek-flash" {
		return 1
	}
	discount := info.PriceData.GroupRatioInfo.UserModelDiscount
	if discount <= 0 || discount >= 1 || math.IsNaN(discount) || math.IsInf(discount, 0) {
		return 1
	}
	return 1 / discount
}
