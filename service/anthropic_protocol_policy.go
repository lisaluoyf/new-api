package service

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

var ErrNativeAnthropicChannel = errors.New("this channel/model does not accept OpenAI-style tool history")

const anthropicHistoryContextKey = "routing_anthropic_history_compatible_v2"

func channelMatchesAnthropicProtocol(c *gin.Context, ch *model.Channel, modelName string) bool {
	if ch == nil {
		return false
	}
	settings := ch.GetSetting()
	restricted := settings.NativeAnthropicOnly // Legacy settings become a history restriction.
	for _, configured := range settings.AnthropicToolHistoryModels {
		if configured == "*" {
			restricted = true
			break
		}
		for _, name := range PricingNameCandidates(modelName, ch.ModelMapping) {
			if configured == name {
				restricted = true
				break
			}
		}
	}
	return !restricted || anthropicHistoryCompatible(c)
}

type anthropicHistoryItem struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	CallID     string `json:"call_id"`
	ToolUseID  string `json:"tool_use_id"`
	Role       string `json:"role"`
	ToolCallID string `json:"tool_call_id"`
	ToolCalls  []struct {
		ID string `json:"id"`
	} `json:"tool_calls"`
	Content json.RawMessage `json:"content"`
}

// Inspect history before conversion without rewriting IDs, signatures, or body.
// A Chat/Responses endpoint alone is not evidence of incompatible history.
func anthropicHistoryCompatible(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return false
	}
	if cached, ok := c.Get(anthropicHistoryContextKey); ok {
		return cached == true
	}
	allowed := false
	defer func() { c.Set(anthropicHistoryContextKey, allowed) }()
	if c.Request.Method != http.MethodPost {
		return false
	}
	switch c.Request.URL.Path {
	case "/v1/messages", "/v1/chat/completions", "/v1/responses":
	default:
		return false
	}
	var request struct {
		Messages []anthropicHistoryItem `json:"messages"`
		Input    json.RawMessage        `json:"input"`
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return false
	}
	body, err := storage.Bytes()
	if err != nil {
		return false
	}
	if _, err = storage.Seek(0, io.SeekStart); err != nil {
		return false
	}
	c.Request.Body = io.NopCloser(storage)
	if common.Unmarshal(body, &request) != nil {
		return false
	}
	for _, item := range request.Messages {
		if !anthropicHistoryItemCompatible(item) {
			return false
		}
	}
	if strings.HasPrefix(strings.TrimSpace(string(request.Input)), "[") {
		var items []anthropicHistoryItem
		if common.Unmarshal(request.Input, &items) != nil {
			return false
		}
		for _, item := range items {
			if !anthropicHistoryItemCompatible(item) {
				return false
			}
		}
	}
	allowed = true
	return true
}

func anthropicHistoryItemCompatible(item anthropicHistoryItem) bool {
	switch item.Type {
	case "tool_use":
		if strings.HasPrefix(item.ID, "call_") {
			return false
		}
	case "tool_result":
		if strings.HasPrefix(item.ToolUseID, "call_") {
			return false
		}
	case "function_call", "function_call_output":
		if strings.HasPrefix(item.CallID, "call_") {
			return false
		}
	}
	if item.Role == "tool" && strings.HasPrefix(item.ToolCallID, "call_") {
		return false
	}
	for _, call := range item.ToolCalls {
		if strings.HasPrefix(call.ID, "call_") {
			return false
		}
	}
	if strings.HasPrefix(strings.TrimSpace(string(item.Content)), "[") {
		var blocks []anthropicHistoryItem
		if common.Unmarshal(item.Content, &blocks) != nil {
			return false
		}
		for _, block := range blocks {
			if !anthropicHistoryItemCompatible(block) {
				return false
			}
		}
	}
	return true
}
