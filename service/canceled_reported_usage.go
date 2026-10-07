package service

import (
	"context"
	"errors"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

// CanceledReportedUsage accepts only counts already validated by the adapter.
// Normal response estimators must never feed this cancellation fallback.
func CanceledReportedUsage(c *gin.Context, info *relaycommon.RelayInfo, usage *dto.Usage, status *relaycommon.StreamStatus) *dto.Usage {
	if c == nil || c.Request == nil || info == nil || usage == nil || info.UserId <= 0 || info.UsePrice ||
		!errors.Is(c.Request.Context().Err(), context.Canceled) || status == nil || status.HasErrors() {
		return nil
	}
	if status.EndReason != relaycommon.StreamEndReasonClientGone && !(status.EndReason == relaycommon.StreamEndReasonScannerErr && errors.Is(status.EndError, context.Canceled)) {
		return nil
	}
	copy := *usage
	copy.UsageSource = "upstream_reported_partial"
	info.CanceledStreamUsage = &copy
	return &copy
}
