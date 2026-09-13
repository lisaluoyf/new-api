package service

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// ClientExclusive marks channels restricted to specific API clients.
type ClientExclusive string

const (
	ClientExclusiveNone       ClientExclusive = ""
	ClientExclusiveCodex      ClientExclusive = "codex"
	ClientExclusiveClaudeCode ClientExclusive = "claude_code"
)

// ClientType classifies the incoming request for routing.
type ClientType string

const (
	ClientTypeGeneric    ClientType = "generic"
	ClientTypeCodex      ClientType = "codex"
	ClientTypeClaudeCode ClientType = "claude_code"
)

var (
	ErrNonClaudeCodeClaudeChannel = errors.New("this model cannot use Claude Code-only channels from non-Claude Code clients")
)

// ExtractClientExclusive reads client_exclusive from channel.Setting JSON.
// Only the explicit client_exclusive field applies; key_group is pricing-only.
func ExtractClientExclusive(setting *string) ClientExclusive {
	if setting == nil || strings.TrimSpace(*setting) == "" {
		return ClientExclusiveNone
	}
	var s struct {
		ClientExclusive string `json:"client_exclusive"`
	}
	if err := common.Unmarshal([]byte(*setting), &s); err != nil {
		return ClientExclusiveNone
	}
	switch strings.ToLower(strings.TrimSpace(s.ClientExclusive)) {
	case string(ClientExclusiveCodex):
		return ClientExclusiveCodex
	case string(ClientExclusiveClaudeCode):
		return ClientExclusiveClaudeCode
	default:
		return ClientExclusiveNone
	}
}

// RequiresClientExclusivePolicy applies to every model, including aliases and future models.
func RequiresClientExclusivePolicy(_ string) bool {
	return true
}

// DetectClaudeCodeClient heuristically identifies Claude Code CLI callers.
func DetectClaudeCodeClient(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	h := c.Request.Header
	if strings.Contains(strings.ToLower(h.Get("User-Agent")), "claude-cli/") {
		return true
	}
	beta := strings.ToLower(h.Get("anthropic-beta"))
	if strings.Contains(beta, "claude-code") && strings.Contains(c.Request.URL.Path, "/v1/messages") {
		return true
	}
	return false
}

// DetectClientType classifies the caller independently of model names.
func DetectClientType(c *gin.Context, _ string) ClientType {
	if DetectCodexClient(c) {
		return ClientTypeCodex
	}
	if DetectClaudeCodeClient(c) {
		return ClientTypeClaudeCode
	}
	return ClientTypeGeneric
}

// InitClientPolicyContext detects client type once per request when policy applies.
func InitClientPolicyContext(c *gin.Context, modelName string) {
	if c == nil || !RequiresClientExclusivePolicy(modelName) {
		return
	}
	if _, ok := common.GetContextKey(c, constant.ContextKeyClientType); ok {
		return
	}
	clientType := DetectClientType(c, modelName)
	common.SetContextKey(c, constant.ContextKeyClientType, string(clientType))
	common.SetContextKey(c, constant.ContextKeyIsCodexClient, clientType == ClientTypeCodex)
}

func clientTypeFromContext(c *gin.Context) ClientType {
	if c == nil {
		return ClientTypeGeneric
	}
	v, ok := common.GetContextKey(c, constant.ContextKeyClientType)
	if !ok {
		return ClientTypeGeneric
	}
	s, _ := v.(string)
	switch ClientType(s) {
	case ClientTypeCodex, ClientTypeClaudeCode:
		return ClientType(s)
	default:
		return ClientTypeGeneric
	}
}

// ChannelMatchesClientPolicy applies one-way client-exclusive rules (Codex + Claude Code).
func ChannelMatchesClientPolicy(setting *string, clientType ClientType, _ string) bool {
	switch ExtractClientExclusive(setting) {
	case ClientExclusiveCodex:
		return clientType == ClientTypeCodex
	case ClientExclusiveClaudeCode:
		return clientType == ClientTypeClaudeCode
	default:
		return true
	}
}

// ChannelPickFilter returns a filter when client-exclusive or gpt-image-2 routing applies.
func ChannelPickFilter(c *gin.Context, modelName string) model.ChannelPickFilter {
	var filters []model.ChannelPickFilter

	if RequiresClientExclusivePolicy(modelName) {
		InitClientPolicyContext(c, modelName)
		clientType := clientTypeFromContext(c)
		filters = append(filters, func(ch *model.Channel) bool {
			if ch == nil {
				return false
			}
			return ChannelMatchesClientPolicy(ch.Setting, clientType, modelName)
		})
	}

	if gptFilter := GptImage2ChannelPickFilter(c, modelName); gptFilter != nil {
		filters = append(filters, gptFilter)
	}

	return composeChannelPickFilters(filters)
}

func composeChannelPickFilters(filters []model.ChannelPickFilter) model.ChannelPickFilter {
	if len(filters) == 0 {
		return nil
	}
	if len(filters) == 1 {
		return filters[0]
	}
	return func(ch *model.Channel) bool {
		for _, f := range filters {
			if f == nil {
				continue
			}
			if !f(ch) {
				return false
			}
		}
		return true
	}
}

// ValidateChannelClientPolicy rejects a pre-selected channel that violates isolation.
func ValidateChannelClientPolicy(c *gin.Context, channel *model.Channel, modelName string) error {
	if channel == nil || !RequiresClientExclusivePolicy(modelName) {
		return nil
	}
	InitClientPolicyContext(c, modelName)
	clientType := clientTypeFromContext(c)
	if ChannelMatchesClientPolicy(channel.Setting, clientType, modelName) {
		return nil
	}
	exclusive := ExtractClientExclusive(channel.Setting)
	if exclusive == ClientExclusiveClaudeCode && clientType != ClientTypeClaudeCode {
		return ErrNonClaudeCodeClaudeChannel
	}
	if exclusive == ClientExclusiveCodex && clientType != ClientTypeCodex {
		return ErrNonCodexCodexChannel
	}
	return ErrNonClaudeCodeClaudeChannel
}

// ClientPolicyChannelError maps empty post-filter selection to a user-facing error.
func ClientPolicyChannelError(c *gin.Context, modelName string) error {
	if !RequiresClientExclusivePolicy(modelName) {
		return nil
	}
	InitClientPolicyContext(c, modelName)
	clientType := clientTypeFromContext(c)
	switch clientType {
	case ClientTypeCodex:
		return fmt.Errorf("%w (%s)", ErrCodexClientNoChannel, modelName)
	case ClientTypeClaudeCode:
		return fmt.Errorf("no Claude Code-compatible channel available for %s", modelName)
	default:
		return fmt.Errorf("no non-exclusive channel available for %s", modelName)
	}
}

// AppendClientExclusiveLogInfo adds client_type for routed models.
func AppendClientExclusiveLogInfo(c *gin.Context, modelName string, other map[string]interface{}) {
	if other == nil || !RequiresClientExclusivePolicy(modelName) {
		return
	}
	InitClientPolicyContext(c, modelName)
	clientType := clientTypeFromContext(c)
	switch clientType {
	case ClientTypeCodex, ClientTypeClaudeCode:
		other["client_type"] = string(clientType)
	default:
		other["client_type"] = "openai_compatible"
		if c != nil && c.Request != nil && strings.Contains(c.Request.URL.Path, "/v1/messages") {
			other["client_type"] = "anthropic_compatible"
		}
	}
}
