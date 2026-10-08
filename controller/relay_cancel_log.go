package controller

import (
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

// Called only for a failed final request, never for an internally canceled hedge
// attempt. Audit persistence is independent of channel health/error-log switches.
func recordCanceledRelayLog(c *gin.Context, info *relaycommon.RelayInfo) {
	if c == nil || info == nil || c.Request == nil || c.Request.Context().Err() == nil || c.GetBool("client_cancel_log_recorded") {
		return
	}
	channelID := c.GetInt("channel_id")
	if channelID <= 0 || info.UserId <= 0 {
		return
	}
	start := info.StartTime
	if start.IsZero() {
		start = time.Now()
	}
	other := map[string]interface{}{
		"error_type":              "client_canceled",
		"error_code":              "client_canceled",
		"status_code":             499,
		"channel_id":              channelID,
		"channel_name":            c.GetString("channel_name"),
		"accounting_status":       "pending_reconciliation",
		"usage_source":            "unavailable",
		"upstream_cost":           nil,
		"received_response_count": info.ReceivedResponseCount,
		"duration_ms":             time.Since(start).Milliseconds(),
		"admin_info":              map[string]interface{}{"use_channel": c.GetStringSlice("use_channel")},
	}
	if c.Request.URL != nil {
		other["request_path"] = c.Request.URL.Path
	}
	if info.FirstResponseTime.After(start) {
		other["frt"] = info.FirstResponseTime.Sub(start).Milliseconds()
	}
	if info.StreamStatus != nil {
		other["stream_status"] = map[string]interface{}{
			"status": "canceled", "end_reason": string(info.StreamStatus.EndReason),
			"error_count": info.StreamStatus.TotalErrorCount(), "summary": info.StreamStatus.Summary(),
		}
		if _, responseID := info.StreamStatus.TerminalUsage(); responseID != "" {
			other["upstream_response_id"] = responseID
		}
	}
	observeCanceledRelayHealth(c, info, other)
	service.AppendBillingSourceInfo(info, other)
	if err := model.RecordErrorLog(c, info.UserId, channelID, info.OriginModelName, c.GetString("token_name"),
		"Client canceled; upstream usage unavailable; cost pending reconciliation (not settled)",
		info.TokenId, int(time.Since(start).Seconds()), info.IsStream, info.UsingGroup, other); err == nil {
		c.Set("client_cancel_log_recorded", true)
	}
	if err := service.ObserveCanceledRelay(c, info); err != nil {
		// The worker retries from durable cancellation audit logs. Observation
		// failure must not change the request's refund/settlement behavior.
		common.SysLog("cancellation observation enqueue failed: " + err.Error())
	}
}

// Observe only the failed final dispatched attempt. The per-attempt error path
// continues to ignore client cancellation, including canceled hedge losers.
func observeCanceledRelayHealth(c *gin.Context, info *relaycommon.RelayInfo, other map[string]interface{}) {
	if c.Request == nil || c.Request.Context().Err() == nil || info.ChannelMeta == nil || c.GetBool("channel_health_hedged_request") || c.GetBool("client_cancel_health_recorded") ||
		c.GetInt("channel_health_attempt_channel_id") != info.ChannelId || info.ChannelId <= 0 {
		return
	}
	raw, exists := c.Get("channel_health_attempt_started_at")
	start, valid := raw.(time.Time)
	if !exists || !valid || start.IsZero() || info.FirstResponseTime.After(start) || !info.LastDataTime.IsZero() {
		return
	}
	elapsed := time.Since(start)
	target := relayProbeTarget(info)
	if !service.ChannelNoResponseCancellationEligible(target, elapsed, info.ReceivedResponseCount) {
		return
	}
	c.Set("client_cancel_health_recorded", true)
	channel := *types.NewChannelError(info.ChannelId, info.ChannelType, c.GetString("channel_name"), info.ChannelIsMultiKey, info.ApiKey, common.GetContextKeyBool(c, constant.ContextKeyChannelAutoBan))
	action, reason := service.EvaluateChannelNoResponseCancellation(channel, target, elapsed, info.ReceivedResponseCount)
	other["channel_health_observation"] = "suspected_upstream_no_response"
	other["channel_health_attempt_duration_ms"] = elapsed.Milliseconds()
	other["channel_health_probe_requested"] = action == service.HealthProbeBeforeDisable
	if action == service.HealthProbeBeforeDisable {
		err := types.NewOpenAIError(errors.New(reason), types.ErrorCode("upstream_no_response_before_cancel"), 502)
		gopool.Go(func() { probeBeforeDisablingChannel(channel, err, reason, target.ModelName, target) })
	}
}
