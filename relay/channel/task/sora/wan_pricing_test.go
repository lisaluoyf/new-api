package sora

import (
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

func TestWanPrimeBillingByResolution(t *testing.T) {
	for _, tc := range []struct {
		resolution string
		seconds    int
		expected   float64
	}{{"480P", 10, 0.68}, {"720P", 10, 1.4}, {"1080P", 10, 2.8}, {"", 0, 0.7}} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Set("task_request", relaycommon.TaskSubmitReq{Model: "wan3.0-video-prime", Resolution: &tc.resolution, Duration: tc.seconds})
		ratios := (&TaskAdaptor{}).EstimateBilling(c, &relaycommon.RelayInfo{OriginModelName: "wan3.0-video-prime", TaskRelayInfo: &relaycommon.TaskRelayInfo{}})
		require.InDelta(t, tc.expected, 0.14*ratios["seconds"]*ratios["size"], 0.0000001)
	}
}
