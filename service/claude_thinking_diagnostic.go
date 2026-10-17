package service

import (
	"math"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const ClaudeThinkingDiagnosticOption = "ClaudeThinkingDiagnostic"

// Off unless an administrator supplies a short-lived, fully scoped option.
// No prompts, messages, headers or credentials are serialized by this observer.
type claudeThinkingDiagnosticConfig struct {
	Enabled     bool   `json:"enabled"`
	UserID      int    `json:"user_id"`
	Model       string `json:"model"`
	ChannelIDs  []int  `json:"channel_ids"`
	ExpiresAt   int64  `json:"expires_at"`
	MaxCaptures int    `json:"max_captures_per_process"`
}

type thinkingParameterSummary struct {
	Present       bool   `json:"thinking_present"`
	Shape         string `json:"thinking_shape"`
	Type          string `json:"thinking_type"`
	Display       string `json:"thinking_display"`
	BudgetPresent bool   `json:"budget_tokens_present"`
	BudgetTokens  *int64 `json:"budget_tokens,omitempty"`
	BudgetInvalid bool   `json:"budget_tokens_invalid,omitempty"`
	Effort        string `json:"output_effort"`
}

type ClaudeThinkingObservation struct {
	channelID int
	model     string
	client    thinkingParameterSummary
	recorded  bool
}

var thinkingDiagnosticBudget struct {
	sync.Mutex
	config string
	used   int
}

func diagnosticEnum(value gjson.Result, allowed ...string) string {
	if !value.Exists() {
		return "absent"
	}
	if value.Type == gjson.Null {
		return "null"
	}
	if value.Type != gjson.String {
		return "invalid"
	}
	for _, option := range allowed {
		if value.Str == option {
			return option
		}
	}
	return "other"
}

func summarizeThinkingParameters(body []byte) thinkingParameterSummary {
	thinking := gjson.GetBytes(body, "thinking")
	shape := "absent"
	if thinking.Exists() {
		shape = "invalid"
		if thinking.IsObject() {
			shape = "object"
		} else if thinking.Type == gjson.Null {
			shape = "null"
		}
	}
	budget := gjson.GetBytes(body, "thinking.budget_tokens")
	summary := thinkingParameterSummary{
		Present: thinking.Exists(), Shape: shape,
		Type:          diagnosticEnum(gjson.GetBytes(body, "thinking.type"), "adaptive", "enabled", "disabled", "between_tools"),
		Display:       diagnosticEnum(gjson.GetBytes(body, "thinking.display"), "omitted", "summarized", "updates"),
		Effort:        diagnosticEnum(gjson.GetBytes(body, "output_config.effort"), "low", "medium", "high", "xhigh", "max", "minimal", "none"),
		BudgetPresent: budget.Exists(),
	}
	if budget.Exists() {
		if budget.Type == gjson.Number && budget.Num >= 0 && budget.Num <= math.MaxInt32 && math.Trunc(budget.Num) == budget.Num {
			value := budget.Int()
			summary.BudgetTokens = &value
		} else {
			summary.BudgetInvalid = true
		}
	}
	return summary
}

func claimThinkingDiagnostic(info *relaycommon.RelayInfo, now int64) bool {
	if info == nil || info.ChannelMeta == nil {
		return false
	}
	common.OptionMapRWMutex.RLock()
	raw := common.OptionMap[ClaudeThinkingDiagnosticOption]
	common.OptionMapRWMutex.RUnlock()
	if raw == "" {
		return false
	}
	var cfg claudeThinkingDiagnosticConfig
	if common.UnmarshalJsonStr(raw, &cfg) != nil || !cfg.Enabled || cfg.UserID <= 0 || cfg.UserID != info.UserId ||
		cfg.Model != "claude-opus-5-5" || cfg.Model != info.OriginModelName || cfg.ExpiresAt <= now || cfg.ExpiresAt-now > 1800 ||
		cfg.MaxCaptures < 1 || cfg.MaxCaptures > 12 {
		return false
	}
	matched := false
	for _, id := range cfg.ChannelIDs {
		if id > 0 && id == info.ChannelId {
			matched = true
			break
		}
	}
	if !matched {
		return false
	}
	thinkingDiagnosticBudget.Lock()
	defer thinkingDiagnosticBudget.Unlock()
	if thinkingDiagnosticBudget.config != raw {
		thinkingDiagnosticBudget.config = raw
		thinkingDiagnosticBudget.used = 0
	}
	if thinkingDiagnosticBudget.used >= cfg.MaxCaptures {
		return false
	}
	thinkingDiagnosticBudget.used++
	return true
}

func BeginClaudeThinkingObservation(c *gin.Context, info *relaycommon.RelayInfo) *ClaudeThinkingObservation {
	if c == nil || !claimThinkingDiagnostic(info, time.Now().Unix()) {
		return nil
	}
	// Reuse the already validated body without GetBodyStorage: that helper
	// rewinds the shared cursor and could disturb an in-flight retry stream.
	cached, exists := c.Get(common.KeyBodyStorage)
	storage, ok := cached.(common.BodyStorage)
	if !exists || !ok || storage.Size() > 8*1024*1024 {
		return nil
	}
	body, err := storage.Bytes()
	if err != nil || !gjson.ValidBytes(body) {
		return nil
	}
	return &ClaudeThinkingObservation{channelID: info.ChannelId, model: info.OriginModelName, client: summarizeThinkingParameters(body)}
}

func (o *ClaudeThinkingObservation) Record(c *gin.Context, upstreamBody []byte, passthrough bool) {
	if o == nil || o.recorded {
		return
	}
	o.recorded = true
	upstream := o.client
	if !passthrough {
		if !gjson.ValidBytes(upstreamBody) {
			return
		}
		upstream = summarizeThinkingParameters(upstreamBody)
	}
	entry := struct {
		ChannelID   int                      `json:"channel_id"`
		Model       string                   `json:"model"`
		Passthrough bool                     `json:"passthrough"`
		Client      thinkingParameterSummary `json:"client"`
		Upstream    thinkingParameterSummary `json:"upstream"`
	}{o.channelID, o.model, passthrough, o.client, upstream}
	raw, err := common.Marshal(entry)
	if err == nil {
		logger.LogInfo(c, "CLAUDE_THINKING_DIAG "+string(raw))
	}
}
