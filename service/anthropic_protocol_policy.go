package service

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

var ErrNativeAnthropicChannel = errors.New("this channel requires native Anthropic Messages requests without OpenAI-converted tool history")

const nativeAnthropicRequestContextKey = "routing_native_anthropic_request"

func channelMatchesAnthropicProtocol(c *gin.Context, ch *model.Channel) bool {
	if ch == nil {
		return false
	}
	if !ch.GetSetting().NativeAnthropicOnly {
		return true
	}
	return isNativeAnthropicRequest(c)
}

// Inspect the original endpoint and history, never User-Agent or other identity
// headers. Cache the result before relay conversion and leave tool IDs intact.
func isNativeAnthropicRequest(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return false
	}
	if cached, ok := c.Get(nativeAnthropicRequestContextKey); ok {
		return cached == true
	}
	allowed := false
	defer func() { c.Set(nativeAnthropicRequestContextKey, allowed) }()
	if c.Request.Method != http.MethodPost || c.Request.URL.Path != "/v1/messages" {
		return false
	}
	var request struct {
		Messages []struct {
			Content   json.RawMessage `json:"content"`
			ToolCalls json.RawMessage `json:"tool_calls"`
		} `json:"messages"`
	}
	if common.UnmarshalBodyReusable(c, &request) != nil {
		return false
	}
	for _, message := range request.Messages {
		if len(message.ToolCalls) > 0 && string(message.ToolCalls) != "null" {
			return false
		}
		if strings.HasPrefix(strings.TrimSpace(string(message.Content)), "[") {
			var blocks []struct {
				Type      string `json:"type"`
				ID        string `json:"id"`
				ToolUseID string `json:"tool_use_id"`
			}
			if common.Unmarshal(message.Content, &blocks) != nil {
				return false
			}
			for _, block := range blocks {
				if (block.Type == "tool_use" && strings.HasPrefix(block.ID, "call_")) ||
					(block.Type == "tool_result" && strings.HasPrefix(block.ToolUseID, "call_")) {
					return false
				}
			}
		}
	}
	allowed = true
	return true
}
