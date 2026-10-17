package relay

import (
	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"math"
)

// Keep the full per-second quote until duration, resolution/audio and account
// coefficients have all been applied. Truncating each intermediate value loses
// quota on rates such as 0.08/0.1 and 0.3/0.1.
func veo31QuotaFromRatios(info *relaycommon.RelayInfo) int {
	quota := info.PriceData.ModelPrice * common.QuotaPerUnit * info.PriceData.GroupRatioInfo.GroupRatio
	for _, ratio := range info.PriceData.OtherRatios {
		quota *= ratio
	}
	return int(math.Round(quota))
}
