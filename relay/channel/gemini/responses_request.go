package gemini

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

func responsesBadRequest(format string, args ...any) *types.NewAPIError {
	return types.NewErrorWithStatusCode(fmt.Errorf(format, args...), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
}

func responsesPresent(v json.RawMessage) bool { return len(v) > 0 && string(v) != "null" }

// Gemini has no Responses storage: require complete history and translate only
// features whose semantics can be preserved by generateContent.
func responsesRequestToGemini(c *gin.Context, info *relaycommon.RelayInfo, r dto.OpenAIResponsesRequest) (*dto.GeminiChatRequest, error) {
	if r.PreviousResponseID != "" {
		return nil, responsesBadRequest("Gemini Responses requires full input history instead of previous_response_id")
	}
	for k, v := range map[string]json.RawMessage{"conversation": r.Conversation, "context_management": r.ContextManagement, "prompt": r.Prompt, "prompt_cache_retention": r.PromptCacheRetention, "enable_thinking": r.EnableThinking, "preset": r.Preset} {
		if responsesPresent(v) {
			return nil, responsesBadRequest("Gemini Responses does not support %s", k)
		}
	}
	if responsesPresent(r.Store) && string(r.Store) != "false" {
		return nil, responsesBadRequest("Gemini Responses supports store=false only")
	}
	if responsesPresent(r.Truncation) && string(r.Truncation) != `"disabled"` {
		return nil, responsesBadRequest("Gemini Responses supports truncation=disabled only")
	}
	if r.TopLogProbs != nil || r.MaxToolCalls != nil {
		return nil, responsesBadRequest("Gemini Responses does not support top_logprobs or max_tool_calls")
	}
	if r.MaxOutputTokens != nil && *r.MaxOutputTokens == 0 {
		return nil, responsesBadRequest("max_output_tokens must be greater than zero")
	}
	if c != nil && c.Request != nil && c.Request.Body != nil {
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			return nil, err
		}
		data, err := storage.Bytes()
		if err != nil {
			return nil, err
		}
		var raw map[string]json.RawMessage
		if err := common.Unmarshal(data, &raw); err != nil {
			return nil, responsesBadRequest("invalid Responses request")
		}
		if responsesPresent(raw["background"]) && string(raw["background"]) != "false" {
			return nil, responsesBadRequest("Gemini Responses does not support background=true")
		}
	}
	in := dto.GeneralOpenAIRequest{Model: r.Model, MaxTokens: r.MaxOutputTokens, Stream: r.Stream, Temperature: r.Temperature, TopP: r.TopP}
	if r.Reasoning != nil {
		in.ReasoningEffort = r.Reasoning.Effort
	}
	if responsesPresent(r.Instructions) {
		var s string
		if err := common.Unmarshal(r.Instructions, &s); err != nil {
			return nil, responsesBadRequest("instructions must be a string")
		}
		in.Messages = append(in.Messages, dto.Message{Role: "system", Content: s})
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
	calls := map[string]bool{}
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
			if role == "developer" {
				role = "system"
			}
			if role != "user" && role != "assistant" && role != "system" {
				return nil, responsesBadRequest("unsupported input role %q", role)
			}
			content, err := responsesContentToChat(item["content"])
			if err != nil {
				return nil, err
			}
			in.Messages = append(in.Messages, dto.Message{Role: role, Content: content})
		case "function_call":
			id, _ := item["call_id"].(string)
			name, _ := item["name"].(string)
			args, ok := item["arguments"].(string)
			var obj map[string]json.RawMessage
			if id == "" || name == "" || !ok || calls[id] || common.UnmarshalJsonStr(args, &obj) != nil || obj == nil {
				return nil, responsesBadRequest("invalid function_call")
			}
			calls[id] = true
			tc := dto.ToolCallResponse{ID: id, Type: "function", Function: dto.FunctionResponse{Name: name, Arguments: args}}
			msg := dto.Message{Role: "assistant"}
			msg.SetToolCalls([]dto.ToolCallResponse{tc})
			in.Messages = append(in.Messages, msg)
		case "function_call_output":
			id, _ := item["call_id"].(string)
			if !calls[id] {
				return nil, responsesBadRequest("function_call_output requires its function_call in input")
			}
			delete(calls, id)
			content, err := responsesContentToChat(item["output"])
			if err != nil {
				return nil, err
			}
			in.Messages = append(in.Messages, dto.Message{Role: "tool", ToolCallId: id, Content: content})
		case "reasoning":
			if s, _ := item["encrypted_content"].(string); s != "" {
				return nil, responsesBadRequest("Gemini Responses does not support foreign encrypted reasoning")
			}
		default:
			return nil, responsesBadRequest("unsupported Gemini Responses input item %q", typ)
		}
	}
	if len(calls) > 0 {
		return nil, responsesBadRequest("function calls in input require matching outputs")
	}
	search := false
	var tools []map[string]json.RawMessage
	if responsesPresent(r.Tools) && common.Unmarshal(r.Tools, &tools) != nil {
		return nil, responsesBadRequest("tools must be an array")
	}
	for _, tool := range tools {
		var typ, name, desc string
		_ = common.Unmarshal(tool["type"], &typ)
		_ = common.Unmarshal(tool["name"], &name)
		_ = common.Unmarshal(tool["description"], &desc)
		if typ == "web_search" || typ == "web_search_preview" {
			for k, v := range tool {
				if k != "type" && responsesPresent(v) {
					return nil, responsesBadRequest("unsupported Gemini web search option %s", k)
				}
			}
			search = true
			continue
		}
		if typ != "function" || name == "" {
			return nil, responsesBadRequest("Gemini Responses supports function and web_search tools only")
		}
		if name == "googleSearch" || name == "codeExecution" || name == "urlContext" {
			return nil, responsesBadRequest("reserved Gemini function name %s", name)
		}
		var params map[string]any
		if responsesPresent(tool["parameters"]) && common.Unmarshal(tool["parameters"], &params) != nil {
			return nil, responsesBadRequest("invalid function parameters")
		}
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		in.Tools = append(in.Tools, dto.ToolCallRequest{Type: "function", Function: dto.FunctionRequest{Name: name, Description: desc, Parameters: params}})
	}
	if responsesPresent(r.ToolChoice) {
		var choice any
		if common.Unmarshal(r.ToolChoice, &choice) != nil {
			return nil, responsesBadRequest("invalid tool_choice")
		}
		switch v := choice.(type) {
		case string:
			if v != "auto" && v != "none" && v != "required" {
				return nil, responsesBadRequest("invalid tool_choice")
			}
			in.ToolChoice = v
		case map[string]any:
			name, ok := v["name"].(string)
			if v["type"] != "function" || !ok || name == "" {
				return nil, responsesBadRequest("tool_choice must select a named function")
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
			in.ToolChoice = map[string]any{"type": "function", "function": map[string]any{"name": name}}
		default:
			return nil, responsesBadRequest("invalid tool_choice")
		}
	}
	if in.ToolChoice == "required" && len(in.Tools) == 0 {
		return nil, responsesBadRequest("required tool_choice needs function tools")
	}
	if responsesPresent(r.ParallelToolCalls) {
		var parallel bool
		if common.Unmarshal(r.ParallelToolCalls, &parallel) != nil {
			return nil, responsesBadRequest("parallel_tool_calls must be boolean")
		}
		if !parallel {
			return nil, responsesBadRequest("Gemini Responses does not support parallel_tool_calls=false")
		}
	}
	if responsesPresent(r.Text) {
		var text struct {
			Format struct {
				Type   string          `json:"type"`
				Name   string          `json:"name"`
				Schema json.RawMessage `json:"schema"`
				Strict *bool           `json:"strict"`
			} `json:"format"`
		}
		if common.Unmarshal(r.Text, &text) != nil {
			return nil, responsesBadRequest("invalid text options")
		}
		switch text.Format.Type {
		case "", "text":
		case "json_object":
			in.ResponseFormat = &dto.ResponseFormat{Type: "json_object"}
		case "json_schema":
			if !responsesPresent(text.Format.Schema) {
				return nil, responsesBadRequest("text.format.schema is required")
			}
			schema, err := common.Marshal(map[string]any{"name": text.Format.Name, "schema": text.Format.Schema, "strict": text.Format.Strict})
			if err != nil {
				return nil, err
			}
			in.ResponseFormat = &dto.ResponseFormat{Type: "json_schema", JsonSchema: schema}
		default:
			return nil, responsesBadRequest("unsupported text.format.type")
		}
	}
	out, err := CovertOpenAI2Gemini(c, in, info)
	if err != nil {
		return nil, err
	}
	if search {
		out.SetTools(append(out.GetTools(), dto.GeminiChatTool{GoogleSearch: map[string]any{}}))
	}
	return out, nil
}

func responsesContentToChat(v any) (any, error) {
	if s, ok := v.(string); ok {
		return s, nil
	}
	parts, ok := v.([]any)
	if !ok || len(parts) == 0 {
		return nil, responsesBadRequest("message content must be text or a nonempty array")
	}
	out := make([]any, 0, len(parts))
	for _, v := range parts {
		p, ok := v.(map[string]any)
		if !ok {
			return nil, responsesBadRequest("content parts must be objects")
		}
		switch p["type"] {
		case "input_text", "output_text":
			s, ok := p["text"].(string)
			if !ok {
				return nil, responsesBadRequest("text must be a string")
			}
			out = append(out, dto.MediaContent{Type: "text", Text: s})
		case "input_image":
			s, ok := p["image_url"].(string)
			if !ok || s == "" {
				return nil, responsesBadRequest("input_image requires image_url")
			}
			out = append(out, dto.MediaContent{Type: "image_url", ImageUrl: &dto.MessageImageUrl{Url: s}})
		default:
			return nil, responsesBadRequest("unsupported Gemini Responses content type %v", p["type"])
		}
	}
	return out, nil
}
