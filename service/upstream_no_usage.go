package service

import (
	"errors"
	"net/http"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

const upstreamNoUsageContextKey = "upstream_no_usage"

// MarkUpstreamNoUsage records a billing-less upstream result for the relay
// controller. Client cancellation is deliberately excluded: that is a client
// lifecycle event, not evidence that the channel is unhealthy.
func MarkUpstreamNoUsage(c *gin.Context, info *relaycommon.RelayInfo) {
	if c == nil || info == nil || c.Request == nil || c.Request.Context().Err() != nil {
		return
	}
	if info.StreamStatus != nil && info.StreamStatus.EndReason == relaycommon.StreamEndReasonClientGone {
		return
	}
	c.Set(upstreamNoUsageContextKey, true)
}

func HasUpstreamNoUsage(c *gin.Context) bool {
	return c != nil && c.GetBool(upstreamNoUsageContextKey)
}

// NewUpstreamNoUsageError turns a successful-but-unbillable upstream result
// into the existing window-fault class so normal health thresholds and the
// disable probe are reused.
func NewUpstreamNoUsageError() *types.NewAPIError {
	return types.NewOpenAIError(
		errors.New("upstream returned no billing usage"),
		types.ErrorCodeDoRequestFailed,
		http.StatusGatewayTimeout,
		types.ErrOptionWithNoRecordErrorLog(),
	)
}
