package relay

import (
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestVeoQuoteRoundsAfterAllRatios(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		base, tier, group, seconds float64
		want                       int
	}{
		{"fast silent operator", .1, .08, .01, 4, 1600},
		{"fast 4K operator", .1, .3, .01, 8, 12000},
		{"lite 720p channel/account", .0125, .0125, 1.05, 8, 52500},
		{"lite 1080p channel/account", .0125, .02, 1.05, 8, 84000},
		{"standard operator", .4, .4, .01, 8, 16000},
		{"fast 4K ordinary account", .1, .3, 1, 8, 1200000},
		{"fractional account coefficient", .1, .08, .01001, 4, 1602},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{}
			info.PriceData.ModelPrice = tc.base
			info.PriceData.GroupRatioInfo.GroupRatio = tc.group
			info.PriceData.OtherRatios = map[string]float64{"size": tc.tier / tc.base, "seconds": tc.seconds}
			require.Equal(t, tc.want, veo31QuotaFromRatios(info))
		})
	}
}
