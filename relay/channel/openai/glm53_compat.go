package openai

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

type glm53ReasoningCompatibilityResult struct {
	Changed bool
	Effort  string
	Source  string
}

var glm53DisabledReasoningValues = map[string]struct{}{
	"none":     {},
	"off":      {},
	"disabled": {},
	"minimal":  {},
}

// Normalize only this always-thinking model family. Missing controls retain the
// upstream default; explicit attempts to disable reasoning use its lowest effort.
func normalizeGLM53Reasoning(modelName, baseURL string, request *dto.GeneralOpenAIRequest) glm53ReasoningCompatibilityResult {
	if request == nil || !isGLM53Model(modelName) {
		return glm53ReasoningCompatibilityResult{}
	}
	effort, source := selectGLM53LegalEffort(request)
	if effort == "" {
		source = disabledGLM53ReasoningSource(request)
		if source != "" {
			effort = "low"
		}
	}
	thinking := normalizeGLM53Thinking(request.THINKING, baseURL)
	if len(thinking) == 0 && len(request.THINKING) == 0 {
		var extra map[string]json.RawMessage
		if common.Unmarshal(request.ExtraBody, &extra) == nil {
			thinking = normalizeGLM53Thinking(extra["thinking"], baseURL)
		}
	}
	changed := request.ReasoningEffort != effort ||
		!bytes.Equal(bytes.TrimSpace(request.THINKING), bytes.TrimSpace(thinking)) ||
		len(request.Reasoning) > 0 || len(request.EnableThinking) > 0 || len(request.Think) > 0 ||
		hasGLM53ReasoningKeys(request.ChatTemplateKwargs) || hasGLM53ReasoningKeys(request.ExtraBody)
	request.ReasoningEffort = effort
	request.THINKING = thinking
	request.Reasoning = nil
	request.EnableThinking = nil
	request.Think = nil
	request.ChatTemplateKwargs = stripGLM53ReasoningKeys(request.ChatTemplateKwargs)
	request.ExtraBody = stripGLM53ReasoningKeys(request.ExtraBody)
	return glm53ReasoningCompatibilityResult{Changed: changed, Effort: effort, Source: source}
}

func isGLM53Model(modelName string) bool {
	name := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(modelName)), "zai-org/")
	return name == "glm-5.3" || name == "glm-5.3-flash"
}

// GMI rejects any top-level thinking object, including type=enabled. Other
// providers can still use clear_thinking to preserve reasoning across turns.
func normalizeGLM53Thinking(raw json.RawMessage, baseURL string) json.RawMessage {
	endpoint, err := url.Parse(baseURL)
	if err == nil && strings.EqualFold(endpoint.Hostname(), "api.gmi-serving.com") {
		return nil
	}
	var object map[string]json.RawMessage
	if common.Unmarshal(raw, &object) != nil {
		return raw
	}
	for _, key := range []string{"type", "enabled", "effort", "reasoning_effort"} {
		delete(object, key)
	}
	if len(object) == 0 {
		return nil
	}
	object["type"] = json.RawMessage(`"enabled"`)
	normalized, err := common.Marshal(object)
	if err != nil {
		return raw
	}
	return normalized
}

// Legal explicit values win in a stable order when clients send multiple
// OpenAI-compatible reasoning representations.
func selectGLM53LegalEffort(request *dto.GeneralOpenAIRequest) (string, string) {
	if effort := legalGLM53Effort(request.ReasoningEffort); effort != "" {
		return effort, "reasoning_effort"
	}
	if effort := legalGLM53Effort(rawObjectString(request.Reasoning, "effort", "reasoning_effort")); effort != "" {
		return effort, "reasoning.effort"
	}
	if effort := legalGLM53Effort(rawObjectString(request.THINKING, "reasoning_effort", "effort")); effort != "" {
		return effort, "thinking.effort"
	}
	if effort := legalGLM53Effort(rawObjectString(request.ChatTemplateKwargs, "reasoning_effort", "effort")); effort != "" {
		return effort, "chat_template_kwargs.reasoning_effort"
	}
	var extra dto.GeneralOpenAIRequest
	if common.Unmarshal(request.ExtraBody, &extra) == nil && len(request.ExtraBody) > 0 {
		extra.ExtraBody = nil
		if effort, source := selectGLM53LegalEffort(&extra); effort != "" {
			return effort, "extra_body." + source
		}
	}
	return "", ""
}

func legalGLM53Effort(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "low", "high", "max":
		return value
	default:
		return ""
	}
}

func disabledGLM53ReasoningSource(request *dto.GeneralOpenAIRequest) string {
	if strings.TrimSpace(request.ReasoningEffort) != "" && legalGLM53Effort(request.ReasoningEffort) == "" {
		return "reasoning_effort_invalid"
	}
	if rawObjectDisabled(request.THINKING) {
		return "thinking_disabled"
	}
	if rawObjectDisabled(request.Reasoning) {
		return "reasoning_disabled"
	}
	if rawFalseOrDisabled(request.EnableThinking) {
		return "enable_thinking_disabled"
	}
	if rawFalseOrDisabled(request.Think) {
		return "think_disabled"
	}
	if rawObjectFalse(request.ChatTemplateKwargs, "enable_thinking") || rawObjectDisabled(request.ChatTemplateKwargs) {
		return "chat_template_kwargs_disabled"
	}
	var extra dto.GeneralOpenAIRequest
	if common.Unmarshal(request.ExtraBody, &extra) == nil && len(request.ExtraBody) > 0 {
		extra.ExtraBody = nil
		if source := disabledGLM53ReasoningSource(&extra); source != "" {
			return "extra_body." + source
		}
	}
	return ""
}

func isDisabledGLM53Value(value string) bool {
	_, ok := glm53DisabledReasoningValues[strings.ToLower(strings.TrimSpace(value))]
	return ok
}

func rawObjectString(raw json.RawMessage, keys ...string) string {
	if len(raw) == 0 {
		return ""
	}
	var object map[string]any
	if common.Unmarshal(raw, &object) != nil {
		return ""
	}
	for _, key := range keys {
		if value, ok := object[key].(string); ok {
			return value
		}
	}
	return ""
}

func rawObjectDisabled(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var object map[string]any
	if common.Unmarshal(raw, &object) != nil {
		return false
	}
	for _, key := range []string{"type", "effort", "reasoning_effort"} {
		if value, ok := object[key].(string); ok && isDisabledGLM53Value(value) {
			return true
		}
	}
	if enabled, ok := object["enabled"].(bool); ok && !enabled {
		return true
	}
	return false
}

func rawFalseOrDisabled(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var value any
	if common.Unmarshal(raw, &value) != nil {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return !typed
	case string:
		return isDisabledGLM53Value(typed)
	default:
		return false
	}
}

func rawObjectFalse(raw json.RawMessage, key string) bool {
	if len(raw) == 0 {
		return false
	}
	var object map[string]any
	if common.Unmarshal(raw, &object) != nil {
		return false
	}
	value, exists := object[key]
	if !exists {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return !typed
	case string:
		return isDisabledGLM53Value(typed)
	default:
		return false
	}
}

var glm53ConflictingObjectKeys = map[string]struct{}{
	"enable_thinking":  {},
	"reasoning_effort": {},
	"thinking":         {},
	"reasoning":        {},
	"think":            {},
}

func hasGLM53ReasoningKeys(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var object map[string]any
	if common.Unmarshal(raw, &object) != nil {
		return false
	}
	for key := range object {
		if _, ok := glm53ConflictingObjectKeys[key]; ok {
			return true
		}
	}
	return false
}

func stripGLM53ReasoningKeys(raw json.RawMessage) json.RawMessage {
	if !hasGLM53ReasoningKeys(raw) {
		return raw
	}
	var object map[string]any
	if common.Unmarshal(raw, &object) != nil {
		return raw
	}
	for key := range glm53ConflictingObjectKeys {
		delete(object, key)
	}
	if len(object) == 0 {
		return nil
	}
	cleaned, err := common.Marshal(object)
	if err != nil {
		return raw
	}
	return cleaned
}
