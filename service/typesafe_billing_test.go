package service

import (
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTypeSafeSettlementUsesInputOnly(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	for _, price := range []float64{0.042, 0.05} {
		info := &relaycommon.RelayInfo{OriginModelName: "jev-latest", RelayFormat: types.RelayFormatTypeSafe,
			StartTime: time.Now(), PriceData: types.PriceData{ModelRatio: price / 2, CompletionRatio: 0,
				GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}}}
		for _, output := range []int{0, 20, 1000000} {
			s := calculateTextQuotaSummary(c, info, &dto.Usage{PromptTokens: 1000000, CompletionTokens: output, TotalTokens: 1000000 + output})
			require.Equal(t, int(price*500000), s.Quota)
			require.Equal(t, output, s.CompletionTokens)
		}
	}
}
