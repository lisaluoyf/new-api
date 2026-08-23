package openai

import (
	"bytes"
	"encoding/json"
	"strings"

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

func normalizeGLM53Reasoning(modelName string, request *dto.GeneralOpenAIRequest) glm53ReasoningCompatibilityResult {
	if request == nil || !strings.EqualFold(strings.TrimSpace(modelName), "glm-5.3") {
		return glm53ReasoningCompatibilityResult{}
	}

	effort, source := selectGLM53LegalEffort(request)
	if effort == "" {
		if disabledSource := disabledGLM53ReasoningSource(request); disabledSource != "" {
			source = disabledSource
		} else {
			source = "model_default"
		}
		effort = "low"
	}

	canonicalThinking := json.RawMessage(`{"type":"enabled"}`)
	changed := request.ReasoningEffort != effort ||
		!bytes.Equal(bytes.TrimSpace(request.THINKING), canonicalThinking) ||
		len(request.Reasoning) > 0 ||
		len(request.EnableThinking) > 0 ||
		len(request.Think) > 0 ||
		hasGLM53ReasoningKeys(request.ChatTemplateKwargs) ||
		hasGLM53ReasoningKeys(request.ExtraBody)

	request.ReasoningEffort = effort
	request.THINKING = canonicalThinking
	request.Reasoning = nil
	request.EnableThinking = nil
	request.Think = nil
	request.ChatTemplateKwargs = stripGLM53ReasoningKeys(request.ChatTemplateKwargs)
	request.ExtraBody = stripGLM53ReasoningKeys(request.ExtraBody)

	return glm53ReasoningCompatibilityResult{
		Changed: changed,
		Effort:  effort,
		Source:  source,
	}
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
	if isDisabledGLM53Value(request.ReasoningEffort) {
		return "reasoning_effort_disabled"
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
	if rawObjectFalse(request.ChatTemplateKwargs, "enable_thinking") {
		return "chat_template_kwargs_disabled"
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
	if json.Unmarshal(raw, &object) != nil {
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
	if json.Unmarshal(raw, &object) != nil {
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
	if json.Unmarshal(raw, &value) != nil {
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
	if json.Unmarshal(raw, &object) != nil {
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
	if json.Unmarshal(raw, &object) != nil {
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
	if json.Unmarshal(raw, &object) != nil {
		return raw
	}
	for key := range glm53ConflictingObjectKeys {
		delete(object, key)
	}
	if len(object) == 0 {
		return nil
	}
	cleaned, err := json.Marshal(object)
	if err != nil {
		return raw
	}
	return cleaned
}
