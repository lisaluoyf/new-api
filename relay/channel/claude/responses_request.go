package claude

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func responsesBadRequest(format string, args ...any) *types.NewAPIError {
	return types.NewErrorWithStatusCode(fmt.Errorf(format, args...), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
}

// This is a stateless protocol bridge. Do not silently drop OpenAI server-side
// state or tools: no provider request (or charge) is valid for those features.
func responsesRequestToClaude(c *gin.Context, r dto.OpenAIResponsesRequest) (*dto.ClaudeRequest, error) {
	if c != nil && c.Request != nil && c.Request.Body != nil {
		var raw map[string]json.RawMessage
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			return nil, responsesBadRequest("invalid Responses request body")
		}
		data, err := storage.Bytes()
		if err != nil {
			return nil, responsesBadRequest("invalid Responses request body")
		}
		if err := common.Unmarshal(data, &raw); err != nil {
			return nil, responsesBadRequest("invalid Responses request JSON")
		}
		if v := raw["reasoning"]; len(v) > 0 && string(v) != "null" {
			var reasoning map[string]any
			if err := common.Unmarshal(v, &reasoning); err != nil {
				return nil, responsesBadRequest("reasoning must be an object")
			}
			for k := range reasoning {
				if k != "effort" && k != "summary" {
					return nil, responsesBadRequest("unsupported Responses reasoning field %s", k)
				}
			}
		}
		if v := raw["background"]; len(v) > 0 && string(v) != "false" && string(v) != "null" {
			return nil, responsesBadRequest("Claude Responses does not support background; use synchronous requests")
		}
		// These unknown fields would otherwise be lost by the shared DTO.
		for _, k := range []string{"reasoning_effort", "thinking", "response_format"} {
			if len(raw[k]) > 0 {
				return nil, responsesBadRequest("use Responses reasoning/text fields instead of %s", k)
			}
		}
	}
	if r.PreviousResponseID != "" {
		return nil, responsesBadRequest("Claude Responses does not support previous_response_id; send the full input history")
	}
	for k, v := range map[string]json.RawMessage{"conversation": r.Conversation, "context_management": r.ContextManagement, "prompt": r.Prompt, "prompt_cache_retention": r.PromptCacheRetention, "enable_thinking": r.EnableThinking, "preset": r.Preset} {
		if len(v) > 0 && string(v) != "null" {
			return nil, responsesBadRequest("Claude Responses does not support %s", k)
		}
	}
	if len(r.Include) > 0 && string(r.Include) != "null" {
		var include []string
		if err := common.Unmarshal(r.Include, &include); err != nil {
			return nil, responsesBadRequest("include must be an array")
		}
		for _, v := range include {
			if v != "reasoning.encrypted_content" && v != "web_search_call.action.sources" {
				return nil, responsesBadRequest("unsupported Claude Responses include %s", v)
			}
		}
	}
	if r.TopLogProbs != nil || r.MaxToolCalls != nil {
		return nil, responsesBadRequest("Claude Responses does not support top_logprobs or max_tool_calls")
	}
	if len(r.Store) > 0 && string(r.Store) != "false" && string(r.Store) != "null" {
		return nil, responsesBadRequest("Claude Responses supports store=false only")
	}
	if len(r.Truncation) > 0 && string(r.Truncation) != `"disabled"` && string(r.Truncation) != "null" {
		return nil, responsesBadRequest("Claude Responses supports truncation=disabled only")
	}
	if r.MaxOutputTokens != nil && *r.MaxOutputTokens == 0 {
		return nil, responsesBadRequest("max_output_tokens must be greater than zero")
	}
	in := dto.GeneralOpenAIRequest{Model: r.Model, MaxTokens: r.MaxOutputTokens, Stream: r.Stream, Temperature: r.Temperature, TopP: r.TopP}
	if r.Reasoning != nil {
		in.ReasoningEffort = r.Reasoning.Effort
		switch r.Reasoning.Summary {
		case "", "auto", "concise", "detailed":
		default:
			return nil, responsesBadRequest("unsupported reasoning.summary")
		}
	}
	if len(r.ParallelToolCalls) > 0 && string(r.ParallelToolCalls) != "null" {
		if err := common.Unmarshal(r.ParallelToolCalls, &in.ParallelTooCalls); err != nil {
			return nil, responsesBadRequest("parallel_tool_calls must be boolean")
		}
	}
	nativeTools := make([]map[string]any, 0)
	if len(r.Tools) > 0 && string(r.Tools) != "null" {
		var tools []struct {
			Type              string          `json:"type"`
			Name              string          `json:"name"`
			Description       string          `json:"description"`
			Parameters        map[string]any  `json:"parameters"`
			Strict            *bool           `json:"strict"`
			SearchContextSize string          `json:"search_context_size"`
			UserLocation      json.RawMessage `json:"user_location"`
			Filters           struct {
				AllowedDomains []string `json:"allowed_domains"`
			} `json:"filters"`
			ExternalWebAccess *bool `json:"external_web_access"`
		}
		if err := common.Unmarshal(r.Tools, &tools); err != nil {
			return nil, responsesBadRequest("tools must be an array of functions")
		}
		for i, t := range tools {
			if t.Type == "web_search" || t.Type == "web_search_preview" {
				if t.ExternalWebAccess != nil && !*t.ExternalWebAccess {
					return nil, responsesBadRequest("Claude web search requires external_web_access=true")
				}
				switch t.SearchContextSize {
				case "", "low", "medium", "high":
				default:
					return nil, responsesBadRequest("invalid search_context_size")
				}
				tool := map[string]any{"type": "web_search_20250305", "name": "web_search"}
				if len(t.UserLocation) > 0 && string(t.UserLocation) != "null" {
					tool["user_location"] = t.UserLocation
				}
				if len(t.Filters.AllowedDomains) > 0 {
					tool["allowed_domains"] = t.Filters.AllowedDomains
				}
				nativeTools = append(nativeTools, tool)
				continue
			}
			if t.Type != "function" || t.Name == "" {
				return nil, responsesBadRequest("Claude Responses supports named function tools only")
			}

			if t.Parameters == nil {
				t.Parameters = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			schema := any(t.Parameters)
			if raw := gjson.GetBytes(r.Tools, fmt.Sprintf("%d.parameters", i)); raw.Type == gjson.JSON {
				schema = json.RawMessage(raw.Raw)
			}
			tool := map[string]any{"name": t.Name, "input_schema": schema}
			if t.Description != "" {
				tool["description"] = t.Description
			}
			if t.Strict != nil {
				tool["strict"] = *t.Strict
			}
			nativeTools = append(nativeTools, tool)
			in.Tools = append(in.Tools, dto.ToolCallRequest{Type: "function", Function: dto.FunctionRequest{Name: t.Name, Description: t.Description, Parameters: t.Parameters}})
		}
	}
	if len(r.ToolChoice) > 0 && string(r.ToolChoice) != "null" {
		var choice any
		if err := common.Unmarshal(r.ToolChoice, &choice); err != nil {
			return nil, responsesBadRequest("invalid tool_choice")
		}
		switch v := choice.(type) {
		case string:
			if v != "auto" && v != "none" && v != "required" {
				return nil, responsesBadRequest("unsupported tool_choice")
			}
			in.ToolChoice = v
		case map[string]any:
			if v["type"] != "function" {
				return nil, responsesBadRequest("tool_choice must select a function")
			}
			name, ok := v["name"].(string)
			if !ok || name == "" {
				return nil, responsesBadRequest("tool_choice.name is required")
			}
			found := false
			for _, t := range in.Tools {
				if t.Function.Name == name {
					found = true
				}
			}
			if !found {
				return nil, responsesBadRequest("tool_choice selects an undefined function")
			}
			in.ToolChoice = map[string]any{"function": map[string]any{"name": name}}
		default:
			return nil, responsesBadRequest("invalid tool_choice")
		}
	}
	var textOptions struct {
		Format struct {
			Type   string         `json:"type"`
			Schema map[string]any `json:"schema"`
			Strict *bool          `json:"strict"`
		} `json:"format"`
		Verbosity string `json:"verbosity"`
	}
	if len(r.Text) > 0 && string(r.Text) != "null" {
		if err := common.Unmarshal(r.Text, &textOptions); err != nil {
			return nil, responsesBadRequest("invalid text options")
		}
		switch textOptions.Format.Type {
		case "", "text":
		case "json_schema":
			if textOptions.Format.Schema == nil {
				return nil, responsesBadRequest("text.format.schema is required")
			}
		default:
			return nil, responsesBadRequest("unsupported text.format.type")
		}
		switch textOptions.Verbosity {
		case "", "low", "medium", "high":
		default:
			return nil, responsesBadRequest("unsupported text.verbosity")
		}

	}
	if in.ToolChoice == "required" && len(in.Tools) == 0 {
		return nil, responsesBadRequest("required tool_choice needs function tools")
	}
	if in.ToolChoice != "auto" && in.ToolChoice != "none" && in.ToolChoice != nil {
		model := r.Model
		for _, base := range []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-fable-5-1", "claude-mythos-5-1"} {
			if model == base || strings.HasPrefix(model, base+"-") {
				return nil, responsesBadRequest("%s does not support forced tool_choice; use auto", base)
			}
		}
	}
	out, err := RequestOpenAI2ClaudeMessage(c, in)
	if err != nil {
		return nil, responsesBadRequest("%v", err)
	}
	if len(r.PromptCacheKey) > 0 && string(r.PromptCacheKey) != "null" {
		var key string
		if err := common.Unmarshal(r.PromptCacheKey, &key); err != nil {
			return nil, responsesBadRequest("prompt_cache_key must be a string")
		}
		if key != "" {
			out.CacheControl = json.RawMessage(`{"type":"ephemeral"}`)
		}
	}
	if len(nativeTools) > 0 {
		out.Tools = nativeTools
	}
	if textOptions.Format.Type == "json_schema" {
		config := map[string]any{}
		if len(out.OutputConfig) > 0 {
			if err := common.Unmarshal(out.OutputConfig, &config); err != nil {
				return nil, err
			}
		}
		config["format"] = map[string]any{"type": "json_schema", "schema": json.RawMessage(gjson.GetBytes(r.Text, "format.schema").Raw)}
		out.OutputConfig, err = common.Marshal(config)
		if err != nil {
			return nil, err
		}
	}
	if len(r.Instructions) > 0 && string(r.Instructions) != "null" {
		var s string
		if err := common.Unmarshal(r.Instructions, &s); err != nil {
			return nil, responsesBadRequest("instructions must be a string")
		}
		if s != "" {
			out.System = []dto.ClaudeMediaMessage{{Type: "text", Text: &s}}
		}
	}
	if textOptions.Verbosity == "low" || textOptions.Verbosity == "high" {
		style := "Keep the final user-facing answer concise."
		if textOptions.Verbosity == "high" {
			style = "Give a detailed final user-facing answer when the task calls for it."
		}
		var system []dto.ClaudeMediaMessage
		if out.System != nil {
			system = out.System.([]dto.ClaudeMediaMessage)
		}
		out.System = append(system, dto.ClaudeMediaMessage{Type: "text", Text: &style})
	}
	var input any
	if err := common.Unmarshal(r.Input, &input); err != nil {
		return nil, responsesBadRequest("input must be a string or array")
	}
	if s, ok := input.(string); ok {
		input = []any{map[string]any{"role": "user", "content": s}}
	}
	items, ok := input.([]any)
	if !ok || len(items) == 0 {
		return nil, responsesBadRequest("input must contain messages")
	}
	// Keep adjacent tool calls/results in a single Claude turn, preserving IDs.
	calls := map[string]bool{}
	searchReplayPending := false
	appendBlocks := func(role string, blocks []dto.ClaudeMediaMessage) {
		n := len(out.Messages)
		if n > 0 && out.Messages[n-1].Role == role {
			out.Messages[n-1].Content = append(out.Messages[n-1].Content.([]dto.ClaudeMediaMessage), blocks...)
		} else {
			out.Messages = append(out.Messages, dto.ClaudeMessage{Role: role, Content: blocks})
		}
	}
	for _, v := range items {
		item, ok := v.(map[string]any)
		if !ok {
			return nil, responsesBadRequest("input items must be objects")
		}
		typ, _ := item["type"].(string)
		switch typ {
		case "", "message":
			role, _ := item["role"].(string)
			if role == "" {
				role = "user"
			}
			if role != "user" && role != "assistant" && role != "system" && role != "developer" {
				return nil, responsesBadRequest("unsupported input role %q", role)
			}
			if role == "user" && searchReplayPending {
				return nil, responsesBadRequest("preserve complete output including encrypted reasoning after web search")
			}
			blocks, err := responsesContentToClaude(item["content"])
			if err != nil {
				return nil, err
			}
			if role == "system" || role == "developer" {
				for _, b := range blocks {
					if b.Type != "text" {
						return nil, responsesBadRequest("system/developer messages must contain text only")
					}
				}
				var system []dto.ClaudeMediaMessage
				if out.System != nil {
					system = out.System.([]dto.ClaudeMediaMessage)
				}
				out.System = append(system, blocks...)
			} else {
				appendBlocks(role, blocks)
			}
		case "function_call":
			id, _ := item["call_id"].(string)
			name, _ := item["name"].(string)
			args, ok := item["arguments"].(string)
			if id == "" || name == "" || !ok || calls[id] {
				return nil, responsesBadRequest("invalid or duplicate function_call")
			}
			var obj map[string]json.RawMessage
			if err := common.UnmarshalJsonStr(args, &obj); err != nil || obj == nil {
				return nil, responsesBadRequest("function_call.arguments must encode an object")
			}
			calls[id] = true
			appendBlocks("assistant", []dto.ClaudeMediaMessage{{Type: "tool_use", Id: id, Name: name, Input: json.RawMessage(args)}})
		case "web_search_call":
			// Its exact provider state follows in the standard reasoning item.
			searchReplayPending = true
			appendBlocks("assistant", []dto.ClaudeMediaMessage{})
		case "function_call_output":
			if searchReplayPending {
				return nil, responsesBadRequest("preserve complete output including encrypted reasoning after web search")
			}
			id, _ := item["call_id"].(string)
			if !calls[id] {
				return nil, responsesBadRequest("function_call_output requires its function_call in input")
			}
			delete(calls, id)
			content := item["output"]
			if _, ok := content.(string); !ok {
				blocks, err := responsesContentToClaude(content)
				if err != nil {
					return nil, err
				}
				content = blocks
			}
			appendBlocks("user", []dto.ClaudeMediaMessage{{Type: "tool_result", ToolUseId: id, Content: content}})
		case "reasoning":
			encrypted, _ := item["encrypted_content"].(string)
			if strings.HasPrefix(encrypted, "anthropic_turn_v1:") {
				content, err := decodeClaudeResponsesTurn(encrypted)
				if err != nil {
					return nil, err
				}
				if len(out.Messages) == 0 || out.Messages[len(out.Messages)-1].Role != "assistant" {
					return nil, responsesBadRequest("Claude turn envelope must follow assistant output")
				}
				out.Messages[len(out.Messages)-1].Content = content
				searchReplayPending = false
			} else if encrypted != "" {
				if !strings.HasPrefix(encrypted, "anthropic_v1:") {
					return nil, responsesBadRequest("reasoning was not produced by Claude Responses")
				}
				data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(encrypted, "anthropic_v1:"))
				if err != nil {
					return nil, responsesBadRequest("invalid Claude reasoning envelope")
				}
				var block dto.ClaudeMediaMessage
				if err := common.Unmarshal(data, &block); err != nil {
					return nil, responsesBadRequest("invalid Claude reasoning envelope")
				}
				// Allow only the exact thinking fields; never let an envelope
				// smuggle messages, tool uses, or unsigned reasoning upstream.
				if block.Type == "thinking" && block.Signature != "" && block.Thinking != nil {
					block = dto.ClaudeMediaMessage{Type: "thinking", Thinking: block.Thinking, Signature: block.Signature}
				} else if block.Type == "redacted_thinking" && len(block.Data) > 0 {
					block = dto.ClaudeMediaMessage{Type: "redacted_thinking", Data: block.Data}
				} else {
					return nil, responsesBadRequest("invalid Claude reasoning envelope")
				}
				appendBlocks("assistant", []dto.ClaudeMediaMessage{block})
			}
			// A public summary without provider state is display-only.

		default:
			return nil, responsesBadRequest("unsupported Responses input item %q", typ)
		}
	}
	if searchReplayPending {
		return nil, responsesBadRequest("missing encrypted web search turn state")
	}
	if len(calls) > 0 {
		return nil, responsesBadRequest("function calls in input require matching outputs")
	}
	if len(out.Messages) == 0 || out.Messages[0].Role != "user" || out.Messages[len(out.Messages)-1].Role != "user" {
		return nil, responsesBadRequest("input must end with a user message or function result")
	}
	return out, nil
}

func responsesContentToClaude(content any) ([]dto.ClaudeMediaMessage, error) {
	if s, ok := content.(string); ok {
		return []dto.ClaudeMediaMessage{{Type: "text", Text: &s}}, nil
	}
	parts, ok := content.([]any)
	if !ok || len(parts) == 0 {
		return nil, responsesBadRequest("message content must be text or a nonempty array")
	}
	out := make([]dto.ClaudeMediaMessage, 0, len(parts))
	for _, v := range parts {
		p, ok := v.(map[string]any)
		if !ok {
			return nil, responsesBadRequest("content parts must be objects")
		}
		b := dto.ClaudeMediaMessage{}
		if cache, ok := p["cache_control"]; ok {
			b.CacheControl, _ = common.Marshal(cache)
		}
		switch p["type"] {
		case "input_text", "output_text":
			s, ok := p["text"].(string)
			if !ok {
				return nil, responsesBadRequest("text must be a string")
			}
			b.Type = "text"
			b.Text = &s
		case "input_image":
			s, ok := p["image_url"].(string)
			if !ok || s == "" {
				return nil, responsesBadRequest("input_image requires image_url")
			}
			b.Type = "image"
			if strings.HasPrefix(s, "data:") {
				parts := strings.SplitN(s, ",", 2)
				if len(parts) != 2 || !strings.HasSuffix(parts[0], ";base64") {
					return nil, responsesBadRequest("image data URL must use base64")
				}
				mime := strings.TrimSuffix(strings.TrimPrefix(parts[0], "data:"), ";base64")
				switch mime {
				case "image/png", "image/jpeg", "image/gif", "image/webp":
				default:
					return nil, responsesBadRequest("unsupported image MIME type")
				}
				if _, err := base64.StdEncoding.DecodeString(parts[1]); err != nil {
					return nil, responsesBadRequest("invalid base64 image")
				}
				b.Source = &dto.ClaudeMessageSource{Type: "base64", MediaType: mime, Data: parts[1]}
			} else {
				u, err := url.Parse(s)
				if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
					return nil, responsesBadRequest("invalid image_url")
				}
				b.Source = &dto.ClaudeMessageSource{Type: "url", Url: s}
			}
		default:
			return nil, responsesBadRequest("unsupported Responses content type %v", p["type"])
		}
		out = append(out, b)
	}
	return out, nil
}
