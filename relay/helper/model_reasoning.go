package helper

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"
)

// ValidateModelReasoning checks the client model before pricing or upstream I/O.
// Explicit effort is never rewritten; an omitted effort retains the model default.
func ValidateModelReasoning(request dto.Request) *types.NewAPIError {
	var model, effort, param string
	var nested []byte
	switch r := request.(type) {
	case *dto.GeneralOpenAIRequest:
		model, effort, param, nested = r.Model, r.ReasoningEffort, "reasoning_effort", r.Reasoning
	case *dto.OpenAIResponsesRequest:
		model, param = r.Model, "reasoning.effort"
		if r.Reasoning != nil {
			effort = r.Reasoning.Effort
		}
	default:
		return nil
	}
	if model != "gpt-6.1-sol" && model != "openai/gpt-6.1-sol" {
		return nil
	}
	// Also check the OpenRouter-style reasoning object accepted by Chat Completions.
	if len(nested) > 0 {
		var reasoning struct {
			Effort string `json:"effort"`
		}
		if common.Unmarshal(nested, &reasoning) == nil && reasoning.Effort != "" {
			if err := validateGPT61Effort(model, reasoning.Effort, "reasoning.effort"); err != nil {
				return err
			}
		}
	}
	return validateGPT61Effort(model, effort, param)
}

func validateGPT61Effort(model, effort, param string) *types.NewAPIError {
	switch effort {
	case "", "low", "medium", "high", "xhigh", "max":
		return nil
	}
	return types.NewRequestParameterError(
		fmt.Sprintf("%s does not support %s=%q. Supported values: low, medium (default), high, xhigh, max. Set low for the lowest reasoning effort, or omit the parameter to use medium. none and minimal are not supported.", model, param, effort),
		param, "unsupported_reasoning_effort",
	)
}

// IsDeterministicParameterError intentionally excludes generic 400s: a provider
// may reject a valid request because of its own channel capabilities or limits.
func IsDeterministicParameterError(err *types.NewAPIError) bool {
	if err == nil || err.StatusCode != http.StatusBadRequest {
		return false
	}
	switch string(err.GetErrorCode()) {
	case "unsupported_reasoning_effort", "unsupported_parameter", "invalid_value", "missing_required_parameter":
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "does not support reasoning effort") ||
		strings.Contains(message, "unsupported reasoning effort")
}
