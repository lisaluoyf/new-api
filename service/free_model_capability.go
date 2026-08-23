package service

import (
	"fmt"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

type FreeModelEndpoint string

const (
	FreeModelEndpointChatCompletions FreeModelEndpoint = "chat_completions"
	FreeModelEndpointResponses       FreeModelEndpoint = "responses"
	FreeModelEndpointMessages        FreeModelEndpoint = "messages"
)

type FreeModelRequirements struct {
	Endpoint         FreeModelEndpoint `json:"endpoint"`
	Text             bool              `json:"text"`
	Vision           bool              `json:"vision"`
	Tools            bool              `json:"tools"`
	CodexClient      bool              `json:"codex_client"`
	JSONObject       bool              `json:"json_object"`
	JSONSchema       bool              `json:"json_schema"`
	RequiredToolCall bool              `json:"required_tool_call"`
	EstimatedInput   int               `json:"estimated_input_tokens"`
	RequestedOutput  int               `json:"requested_output_tokens"`
	AffinityKey      string            `json:"-"`
	Schema           map[string]any    `json:"-"`
}

func (r FreeModelRequirements) Names() []string {
	names := []string{"text", "endpoint:" + string(r.Endpoint)}
	if r.Vision {
		names = append(names, "vision")
	}
	if r.Tools {
		names = append(names, "tools")
	}
	if r.CodexClient {
		names = append(names, "client:codex")
	}
	if r.JSONObject {
		names = append(names, "json_object")
	}
	if r.JSONSchema {
		names = append(names, "json_schema")
	}
	return names
}

func (r FreeModelRequirements) TotalContextTokens() int {
	return r.EstimatedInput + r.RequestedOutput
}

// ParseFreeModelRequirements parses only routing-relevant fields. It never
// stores the request body in the route trace.
func ParseFreeModelRequirements(path string, body []byte) (FreeModelRequirements, error) {
	req := FreeModelRequirements{Text: true, Endpoint: freeModelEndpointForPath(path)}
	var root map[string]any
	if err := common.Unmarshal(body, &root); err != nil {
		return req, fmt.Errorf("invalid JSON request: %w", err)
	}
	req.Vision = containsImageContent(root)
	if tools, ok := root["tools"].([]any); ok && len(tools) > 0 {
		req.Tools = true
	}
	if functions, ok := root["functions"].([]any); ok && len(functions) > 0 {
		req.Tools = true
	}
	req.AffinityKey = stringValue(root["prompt_cache_key"])
	req.RequiredToolCall = isRequiredToolChoice(root["tool_choice"]) || isRequiredToolChoice(root["function_call"])

	format := mapValue(root["response_format"])
	if strings.HasSuffix(path, "/responses") {
		if text := mapValue(root["text"]); text != nil {
			format = mapValue(text["format"])
		}
	} else if strings.HasSuffix(path, "/messages") {
		if output := mapValue(root["output_config"]); output != nil {
			format = mapValue(output["format"])
		}
		if format == nil {
			format = mapValue(root["output_format"])
		}
	}
	if format != nil {
		switch strings.ToLower(stringValue(format["type"])) {
		case "json_object", "json":
			req.JSONObject = true
		case "json_schema":
			req.JSONSchema = true
			req.JSONObject = true
			var schema map[string]any
			if wrapper := mapValue(format["json_schema"]); wrapper != nil {
				schema = mapValue(wrapper["schema"])
			}
			if schema == nil {
				schema = mapValue(format["schema"])
			}
			req.Schema = schema
		}
	}
	if req.JSONSchema && req.Schema == nil {
		return req, fmt.Errorf("response_format json_schema requires a schema")
	}

	for _, key := range []string{"max_output_tokens", "max_completion_tokens", "max_tokens"} {
		if n := intValue(root[key]); n > req.RequestedOutput {
			req.RequestedOutput = n
		}
	}
	// Counting the routing-relevant JSON is conservative and includes tools and
	// image metadata. The established tokenizer is used with a stable tokenizer.
	copyRoot := make(map[string]any, len(root))
	for key, value := range root {
		if key != "model" && key != "stream" && key != "max_tokens" && key != "max_output_tokens" && key != "max_completion_tokens" {
			copyRoot[key] = sanitizeFreeModelTokenValue(value)
		}
	}
	encoded, err := common.Marshal(copyRoot)
	if err != nil {
		return req, err
	}
	req.EstimatedInput = CountTextToken(string(encoded), "gpt-4o")
	return req, nil
}

func freeModelEndpointForPath(path string) FreeModelEndpoint {
	switch {
	case strings.HasSuffix(path, "/responses"):
		return FreeModelEndpointResponses
	case strings.HasSuffix(path, "/messages"):
		return FreeModelEndpointMessages
	default:
		return FreeModelEndpointChatCompletions
	}
}

// Image bytes are not language-model text tokens. Keeping a short marker still
// accounts for the image block itself without rejecting base64 vision requests
// merely because their transport representation is large.
func sanitizeFreeModelTokenValue(value any) any {
	switch current := value.(type) {
	case []any:
		out := make([]any, len(current))
		for i, item := range current {
			out[i] = sanitizeFreeModelTokenValue(item)
		}
		return out
	case map[string]any:
		t := strings.ToLower(stringValue(current["type"]))
		if t == "image_url" || t == "input_image" || t == "image" || t == "base64_image" {
			return map[string]any{"type": t, "image": "[image]"}
		}
		out := make(map[string]any, len(current))
		for key, item := range current {
			if key == "image_url" {
				out[key] = "[image]"
			} else {
				out[key] = sanitizeFreeModelTokenValue(item)
			}
		}
		return out
	default:
		return value
	}
}

func mapValue(value any) map[string]any {
	m, _ := value.(map[string]any)
	return m
}

func stringValue(value any) string {
	s, _ := value.(string)
	return strings.TrimSpace(s)
}

func intValue(value any) int {
	switch n := value.(type) {
	case float64:
		if n > 0 {
			return int(n)
		}
	case int:
		if n > 0 {
			return n
		}
	}
	return 0
}

func isRequiredToolChoice(value any) bool {
	if strings.EqualFold(stringValue(value), "required") {
		return true
	}
	m := mapValue(value)
	if m == nil {
		return false
	}
	t := strings.ToLower(stringValue(m["type"]))
	return t == "required" || t == "any" || t == "tool" || t == "function"
}

func containsImageContent(value any) bool {
	switch current := value.(type) {
	case []any:
		for _, item := range current {
			if containsImageContent(item) {
				return true
			}
		}
	case map[string]any:
		t := strings.ToLower(stringValue(current["type"]))
		if t == "image_url" || t == "input_image" || t == "image" || t == "base64_image" {
			return true
		}
		if _, ok := current["image_url"]; ok {
			return true
		}
		for key, item := range current {
			// Tool schemas can contain properties named image without carrying an image.
			if key == "tools" || key == "functions" || key == "response_format" {
				continue
			}
			if containsImageContent(item) {
				return true
			}
		}
	}
	return false
}

func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}
