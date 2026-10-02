package relay

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/channel/task/apimartvideo"
	"github.com/QuantumNous/new-api/relay/channel/task/doubao"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSeedanceTariffUsesSameWorkAcrossProviderProtocols(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	old, exists := common.OptionMap["VideoModelPricing"]
	common.OptionMap["VideoModelPricing"] = `{"seedance-2.0":{"base_price":0.1,"prices":{"720P":0.1,"720P-input":0.06}},"seedance-2.5":{"base_price":0.1,"prices":{"720P":0.1,"720P-input":0.06}},"seedance-2.0-fast":{"base_price":0.1,"prices":{"720P":0.1,"720P-input":0.06}},"seedance-2.0-mini":{"base_price":0.1,"prices":{"720P":0.1,"720P-input":0.06}}}`
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if exists {
			common.OptionMap["VideoModelPricing"] = old
		} else {
			delete(common.OptionMap, "VideoModelPricing")
		}
	})
	for _, name := range []string{"seedance-2.0", "seedance-2.5", "seedance-2.0-fast", "seedance-2.0-mini"} {
		for _, provider := range []string{"apimart", "doubao"} {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/videos/generations", strings.NewReader(`{"model":"`+name+`","prompt":"scene","duration":5,"resolution":"720p","video_urls":["https://example.com/ref.mp4"]}`))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set("seedance_video_input_seconds", 7)
			info := &relaycommon.RelayInfo{OriginModelName: name, TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
			if provider == "apimart" {
				require.Nil(t, (&apimartvideo.TaskAdaptor{}).ValidateRequestAndSetAction(c, info))
			} else {
				require.Nil(t, (&doubao.TaskAdaptor{}).ValidateRequestAndSetAction(c, info))
			}
			ratios, err := service.PrepareSeedanceTaskBilling(c, info)
			require.NoError(t, err, name+provider)
			require.Equal(t, 12.0, ratios["seconds"], name+provider)
			require.InDelta(t, .6, ratios["size"], 1e-9, name+provider)
		}
	}
}
