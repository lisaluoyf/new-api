package gemini

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"
)

// Explicitly scope the compatibility rules to verified model profiles. Older
// Gemini models retain their existing budget and model-suffix adapters.
func normalizeThinkingLevel(request dto.GeneralOpenAIRequest, model string) (*dto.GeminiThinkingConfig, bool, error) {
	model = strings.TrimPrefix(model, "google/")
	if model != "gemini-4-argon" && model != "gemini-3.8-flash" {
		return nil, false, nil
	}
	badRequest := func(message string) (*dto.GeminiThinkingConfig, bool, error) {
		return nil, true, types.NewErrorWithStatusCode(fmt.Errorf("%s", message), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	var fields map[string]json.RawMessage
	if len(request.ExtraBody) > 0 {
		var extra map[string]json.RawMessage
		if err := common.Unmarshal(request.ExtraBody, &extra); err != nil {
			return badRequest("extra_body must be an object")
		}
		if raw, exists := extra["google"]; exists {
			var google map[string]json.RawMessage
			if err := common.Unmarshal(raw, &google); err != nil || google == nil {
				return badRequest("extra_body.google must be an object")
			}
			if _, exists := google["thinkingConfig"]; exists {
				return badRequest("use extra_body.google.thinking_config instead of thinkingConfig")
			}
			if raw, exists := google["thinking_config"]; exists {
				if err := common.Unmarshal(raw, &fields); err != nil || fields == nil {
					return badRequest("extra_body.google.thinking_config must be an object")
				}
			}
		}
	}
	_, hasLevel := fields["thinking_level"]
	_, hasBudget := fields["thinking_budget"]
	if _, exists := fields["thinkingLevel"]; exists {
		return badRequest("use thinking_level instead of thinkingLevel in extra_body.google.thinking_config")
	}
	if _, exists := fields["thinkingBudget"]; exists {
		return badRequest("use thinking_budget instead of thinkingBudget in extra_body.google.thinking_config")
	}
	if request.ReasoningEffort != "" && (hasLevel || hasBudget) {
		return badRequest("reasoning_effort cannot be combined with thinking_level or thinking_budget")
	}
	if model == "gemini-4-argon" && hasLevel && hasBudget {
		return badRequest("gemini-4-argon cannot combine thinking_level and thinking_budget")
	}
	var config *dto.GeminiThinkingConfig
	if len(fields) > 0 {
		config = &dto.GeminiThinkingConfig{}
		if hasBudget {
			if err := common.Unmarshal(fields["thinking_budget"], &config.ThinkingBudget); err != nil || config.ThinkingBudget == nil {
				return badRequest("thinking_budget must be an integer")
			}
			config.IncludeThoughts = *config.ThinkingBudget > 0
		}
		if raw, exists := fields["include_thoughts"]; exists {
			var value *bool
			if err := common.Unmarshal(raw, &value); err != nil || value == nil {
				return badRequest("include_thoughts must be a boolean")
			}
			config.IncludeThoughts = *value
		}
		if hasLevel {
			if err := common.Unmarshal(fields["thinking_level"], &config.ThinkingLevel); err != nil {
				return badRequest("thinking_level must be a string")
			}
			switch config.ThinkingLevel {
			case "low", "medium", "high":
			default:
				return badRequest(model + " thinking_level must be low, medium or high")
			}
		}
	}
	if effort := request.ReasoningEffort; effort != "" {
		if model == "gemini-4-argon" && effort == "minimal" {
			effort = "low" // Google's OpenAI compatibility mapping.
		}
		switch effort {
		case "low", "medium", "high":
		default:
			return badRequest("unsupported reasoning_effort for " + model + ": " + request.ReasoningEffort)
		}
		if config == nil {
			config = &dto.GeminiThinkingConfig{}
		}
		config.ThinkingLevel = effort
	}
	return config, true, nil
}
