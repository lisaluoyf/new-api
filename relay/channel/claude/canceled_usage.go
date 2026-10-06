package claude

import (
	"context"
	"errors"
	"strconv"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// Keep a separate copy of provider-reported usage: the normal finalizer may
// fill missing fields using estimates. Those estimates must never authorize a
// cancellation charge. Presence checks preserve explicitly reported zeroes.
func captureClaudeReportedUsage(info *relaycommon.RelayInfo, state *ClaudeResponseInfo, data string, event *dto.ClaudeResponse) {
	if state.reportedUsageInvalid {
		return
	}
	prefix := "usage."
	if event.Type == "message_start" {
		if state.reportedUsage != nil {
			state.reportedUsageInvalid = true
			return
		}
		prefix = "message.usage."
		if !gjson.Get(data, prefix+"input_tokens").Exists() || gjson.Get(data, "message.id").String() == "" {
			return
		}
		state.reportedUsage = &dto.Usage{UsageSemantic: "anthropic", UsageSource: "upstream_reported_partial"}
		if info.StreamStatus != nil {
			info.StreamStatus.RecordResponseID(gjson.Get(data, "message.id").String())
		}
	} else if event.Type != "message_delta" || state.reportedUsage == nil {
		return
	}
	usage := state.reportedUsage
	fields := map[string]*int{
		"input_tokens":                             &usage.PromptTokens,
		"output_tokens":                            &usage.CompletionTokens,
		"cache_read_input_tokens":                  &usage.PromptTokensDetails.CachedTokens,
		"cache_creation_input_tokens":              &usage.PromptTokensDetails.CachedCreationTokens,
		"cache_creation.ephemeral_5m_input_tokens": &usage.ClaudeCacheCreation5mTokens,
		"cache_creation.ephemeral_1h_input_tokens": &usage.ClaudeCacheCreation1hTokens,
	}
	for field, dst := range fields {
		value := gjson.Get(data, prefix+field)
		if !value.Exists() {
			continue
		}
		count, err := strconv.ParseInt(value.Raw, 10, 32)
		if value.Type != gjson.Number || err != nil || count < 0 {
			state.reportedUsageInvalid = true
			return
		}
		if field == "output_tokens" && int(count) < *dst {
			state.reportedUsageInvalid = true
			return
		}
		*dst = int(count)
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	if event.Type == "message_delta" && gjson.Get(data, "usage.output_tokens").Exists() {
		state.reportedTerminal = true
		if info.StreamStatus != nil {
			// Record before forwarding: a disconnect during the terminal write
			// cannot erase the already parsed usage evidence.
			info.StreamStatus.RecordTerminalUsage("message_delta", state.ResponseId)
		}
	}
}

func canceledClaudeReportedUsage(c *gin.Context, info *relaycommon.RelayInfo, state *ClaudeResponseInfo, status *relaycommon.StreamStatus) *dto.Usage {
	if c == nil || c.Request == nil || !errors.Is(c.Request.Context().Err(), context.Canceled) ||
		info.UserId <= 0 || info.UsePrice || state.reportedUsage == nil || state.reportedUsageInvalid ||
		status == nil || status.HasErrors() {
		return nil
	}
	if status.EndReason != relaycommon.StreamEndReasonClientGone &&
		!(status.EndReason == relaycommon.StreamEndReasonScannerErr && errors.Is(status.EndError, context.Canceled)) {
		return nil
	}
	usage := *state.reportedUsage
	info.CanceledStreamUsage = &usage
	return &usage
}
