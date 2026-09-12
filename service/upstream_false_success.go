package service

import (
	"strings"

	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

const upstreamFalseSuccessSummaryContextKey = "upstream_false_success_summary"

type upstreamFalseSuccessSummary struct {
	Count      int
	Triggers   []string
	ErrorCodes []string
}

func RecordUpstreamFalseSuccessAttempt(c *gin.Context, diagnostic *types.UpstreamFalseSuccessDiagnostic) {
	if c == nil || diagnostic == nil {
		return
	}
	summary, _ := c.Get(upstreamFalseSuccessSummaryContextKey)
	state, _ := summary.(upstreamFalseSuccessSummary)
	state.Count++
	state.Triggers = appendUniqueDiagnosticValue(state.Triggers, diagnostic.Trigger)
	state.ErrorCodes = appendUniqueDiagnosticValue(state.ErrorCodes, diagnostic.ErrorCode)
	c.Set(upstreamFalseSuccessSummaryContextKey, state)
}

func AppendUpstreamFalseSuccessSummary(c *gin.Context, adminInfo map[string]interface{}) {
	if c == nil || adminInfo == nil {
		return
	}
	value, ok := c.Get(upstreamFalseSuccessSummaryContextKey)
	if !ok {
		return
	}
	state, ok := value.(upstreamFalseSuccessSummary)
	if !ok || state.Count <= 0 {
		return
	}
	adminInfo["upstream_false_success"] = map[string]interface{}{
		"triggered":   true,
		"count":       state.Count,
		"triggers":    append([]string(nil), state.Triggers...),
		"error_codes": append([]string(nil), state.ErrorCodes...),
	}
}

func appendUniqueDiagnosticValue(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
