package service

import (
	"fmt"
	"math"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

func ValidateRelayStreamEnd(c *gin.Context, info *relaycommon.RelayInfo, status *relaycommon.StreamStatus, terminalFrame bool) *types.NewAPIError {
	if info == nil {
		return nil
	}
	if status == nil {
		return types.NewOpenAIError(fmt.Errorf("upstream stream did not start"), types.ErrorCodeBadResponse, http.StatusBadGateway)
	}
	if status.EndReason == relaycommon.StreamEndReasonDone && !status.HasErrors() {
		return nil
	}
	if status.EndReason == relaycommon.StreamEndReasonEOF && terminalFrame && !status.HasErrors() {
		return nil
	}
	message := "upstream stream ended abnormally: " + status.Summary()
	return types.NewOpenAIError(fmt.Errorf("%s", message), types.ErrorCodeBadResponse, http.StatusBadGateway)
}

func freeModelValidationError(code types.ErrorCode, message string) *types.NewAPIError {
	return types.NewOpenAIError(fmt.Errorf("%s", message), code, http.StatusBadGateway)
}

func ValidateFreeModelOpenAIResponse(c *gin.Context, response *dto.OpenAITextResponse) *types.NewAPIError {
	if c == nil || response == nil || !IsFreeModel(c.GetString("original_model")) {
		return nil
	}
	requirements, _ := FreeModelRequirementsFromContext(c)
	if len(response.Choices) == 0 {
		return freeModelValidationError(types.ErrorCode("empty_response"), "free model returned no choices")
	}
	for _, choice := range response.Choices {
		finish := strings.TrimSpace(choice.FinishReason)
		switch finish {
		case constant.FinishReasonStop, constant.FinishReasonLength, constant.FinishReasonToolCalls, constant.FinishReasonFunctionCall:
		case constant.FinishReasonContentFilter:
			// A safety refusal is a valid terminal response and must not silently
			// switch to a different provider.
			continue
		default:
			return freeModelValidationError(types.ErrorCode("invalid_finish_reason"), "free model returned an invalid finish_reason")
		}
		var toolCalls []dto.ToolCallResponse
		if len(choice.Message.ToolCalls) > 0 {
			if err := common.Unmarshal(choice.Message.ToolCalls, &toolCalls); err != nil {
				return freeModelValidationError(types.ErrorCode("invalid_tool_arguments"), "free model returned malformed tool calls")
			}
		}
		for _, call := range toolCalls {
			var arguments any
			if strings.TrimSpace(call.Function.Arguments) == "" || common.Unmarshal([]byte(call.Function.Arguments), &arguments) != nil {
				return freeModelValidationError(types.ErrorCode("invalid_tool_arguments"), "free model returned invalid tool arguments")
			}
		}
		content := strings.TrimSpace(choice.Message.StringContent())
		if content == "" && len(toolCalls) == 0 {
			return freeModelValidationError(types.ErrorCode("empty_response"), "free model returned empty content")
		}
		if requirements.RequiredToolCall && len(toolCalls) == 0 {
			return freeModelValidationError(types.ErrorCode("missing_required_tool_call"), "free model did not return the required tool call")
		}
		if requirements.JSONObject || requirements.JSONSchema {
			var document any
			if content == "" || common.Unmarshal([]byte(content), &document) != nil {
				return freeModelValidationError(types.ErrorCode("invalid_json"), "free model returned invalid JSON")
			}
			if requirements.JSONSchema && requirements.Schema != nil {
				if err := validateJSONSchemaValue(document, requirements.Schema, requirements.Schema, "$"); err != nil {
					return freeModelValidationError(types.ErrorCode("schema_validation_failed"), "free model JSON did not match the requested schema: "+err.Error())
				}
			}
		}
	}
	return nil
}

func ValidateFreeModelResponsesResponse(c *gin.Context, response *dto.OpenAIResponsesResponse) *types.NewAPIError {
	if c == nil || response == nil || !IsFreeModel(c.GetString("original_model")) {
		return nil
	}
	requirements, _ := FreeModelRequirementsFromContext(c)
	if len(response.Output) == 0 {
		return freeModelValidationError(types.ErrorCode("empty_response"), "free model returned no output")
	}
	var text strings.Builder
	toolCalls := 0
	for _, output := range response.Output {
		for _, content := range output.Content {
			if content.Type == "output_text" || content.Type == "text" {
				text.WriteString(content.Text)
			}
		}
		if output.Type == "function_call" || output.Type == "tool_call" {
			toolCalls++
			arguments := output.ArgumentsString()
			var decoded any
			if strings.TrimSpace(arguments) == "" || common.Unmarshal([]byte(arguments), &decoded) != nil {
				return freeModelValidationError(types.ErrorCode("invalid_tool_arguments"), "free model returned invalid tool arguments")
			}
		}
	}
	content := strings.TrimSpace(text.String())
	if content == "" && toolCalls == 0 {
		return freeModelValidationError(types.ErrorCode("empty_response"), "free model returned empty output")
	}
	if requirements.RequiredToolCall && toolCalls == 0 {
		return freeModelValidationError(types.ErrorCode("missing_required_tool_call"), "free model did not return the required tool call")
	}
	if requirements.JSONObject || requirements.JSONSchema {
		var document any
		if content == "" || common.Unmarshal([]byte(content), &document) != nil {
			return freeModelValidationError(types.ErrorCode("invalid_json"), "free model returned invalid JSON")
		}
		if requirements.JSONSchema && requirements.Schema != nil {
			if err := validateJSONSchemaValue(document, requirements.Schema, requirements.Schema, "$"); err != nil {
				return freeModelValidationError(types.ErrorCode("schema_validation_failed"), "free model JSON did not match the requested schema: "+err.Error())
			}
		}
	}
	return nil
}

// validateJSONSchemaValue implements the validation keywords used by the
// OpenAI structured-output subset without introducing a database- or encoder-
// specific dependency.
func validateJSONSchemaValue(value any, schema, root map[string]any, path string) error {
	return validateJSONSchemaValueDepth(value, schema, root, path, 0)
}

func validateJSONSchemaValueDepth(value any, schema, root map[string]any, path string, depth int) error {
	if depth > 100 {
		return fmt.Errorf("schema nesting exceeds limit")
	}
	if ref := stringValue(schema["$ref"]); strings.HasPrefix(ref, "#/") {
		resolved, err := resolveLocalSchemaRef(root, ref)
		if err != nil {
			return err
		}
		return validateJSONSchemaValueDepth(value, resolved, root, path, depth+1)
	}
	if expected, ok := schema["const"]; ok && !reflect.DeepEqual(value, expected) {
		return fmt.Errorf("%s does not match const", path)
	}
	if enum, ok := schema["enum"].([]any); ok {
		matched := false
		for _, item := range enum {
			if reflect.DeepEqual(value, item) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s is not in enum", path)
		}
	}
	if variants, ok := schema["allOf"].([]any); ok {
		for _, raw := range variants {
			if sub := mapValue(raw); sub != nil {
				if err := validateJSONSchemaValueDepth(value, sub, root, path, depth+1); err != nil {
					return err
				}
			}
		}
	}
	for _, keyword := range []string{"anyOf", "oneOf"} {
		if variants, ok := schema[keyword].([]any); ok {
			matches := 0
			for _, raw := range variants {
				if sub := mapValue(raw); sub != nil && validateJSONSchemaValueDepth(value, sub, root, path, depth+1) == nil {
					matches++
				}
			}
			if matches == 0 || (keyword == "oneOf" && matches != 1) {
				return fmt.Errorf("%s does not match %s", path, keyword)
			}
		}
	}
	typesAllowed := make([]string, 0)
	if t := stringValue(schema["type"]); t != "" {
		typesAllowed = append(typesAllowed, t)
	}
	if list, ok := schema["type"].([]any); ok {
		for _, item := range list {
			if t := stringValue(item); t != "" {
				typesAllowed = append(typesAllowed, t)
			}
		}
	}
	if len(typesAllowed) > 0 {
		matched := false
		for _, expected := range typesAllowed {
			if jsonSchemaTypeMatches(value, expected) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s has the wrong type", path)
		}
	}
	switch current := value.(type) {
	case map[string]any:
		if required, ok := schema["required"].([]any); ok {
			for _, raw := range required {
				key := stringValue(raw)
				if _, exists := current[key]; !exists {
					return fmt.Errorf("%s.%s is required", path, key)
				}
			}
		}
		properties := mapValue(schema["properties"])
		for key, item := range current {
			if child := mapValue(properties[key]); child != nil {
				if err := validateJSONSchemaValueDepth(item, child, root, path+"."+key, depth+1); err != nil {
					return err
				}
				continue
			}
			if additional, exists := schema["additionalProperties"]; exists {
				if allowed, ok := additional.(bool); ok && !allowed {
					return fmt.Errorf("%s.%s is not allowed", path, key)
				}
				if child := mapValue(additional); child != nil {
					if err := validateJSONSchemaValueDepth(item, child, root, path+"."+key, depth+1); err != nil {
						return err
					}
				}
			}
		}
	case []any:
		if min := intValue(schema["minItems"]); min > 0 && len(current) < min {
			return fmt.Errorf("%s has too few items", path)
		}
		if max := intValue(schema["maxItems"]); max > 0 && len(current) > max {
			return fmt.Errorf("%s has too many items", path)
		}
		if itemSchema := mapValue(schema["items"]); itemSchema != nil {
			for i, item := range current {
				if err := validateJSONSchemaValueDepth(item, itemSchema, root, fmt.Sprintf("%s[%d]", path, i), depth+1); err != nil {
					return err
				}
			}
		}
	case string:
		length := utf8.RuneCountInString(current)
		if min := intValue(schema["minLength"]); min > 0 && length < min {
			return fmt.Errorf("%s is too short", path)
		}
		if max := intValue(schema["maxLength"]); max > 0 && length > max {
			return fmt.Errorf("%s is too long", path)
		}
		if pattern := stringValue(schema["pattern"]); pattern != "" {
			matched, err := regexp.MatchString(pattern, current)
			if err != nil || !matched {
				return fmt.Errorf("%s does not match pattern", path)
			}
		}
	case float64:
		if minimum, ok := schema["minimum"].(float64); ok && current < minimum {
			return fmt.Errorf("%s is below minimum", path)
		}
		if maximum, ok := schema["maximum"].(float64); ok && current > maximum {
			return fmt.Errorf("%s is above maximum", path)
		}
	}
	return nil
}

func jsonSchemaTypeMatches(value any, expected string) bool {
	switch expected {
	case "null":
		return value == nil
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		n, ok := value.(float64)
		return ok && math.Trunc(n) == n
	}
	return true
}

func resolveLocalSchemaRef(root map[string]any, ref string) (map[string]any, error) {
	var current any = root
	for _, segment := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		segment = strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
		m := mapValue(current)
		if m == nil {
			return nil, fmt.Errorf("invalid schema ref %s", ref)
		}
		current = m[segment]
	}
	resolved := mapValue(current)
	if resolved == nil {
		return nil, fmt.Errorf("unresolved schema ref %s", ref)
	}
	return resolved, nil
}
