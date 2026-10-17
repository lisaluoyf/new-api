package types

import "net/http"

// NewRequestParameterError exposes only a gateway-authored validation message.
// Upstream errors continue through the existing redaction policy.
func NewRequestParameterError(message, param, code string) *NewAPIError {
	err := WithOpenAIError(OpenAIError{Message: message, Type: "invalid_request_error", Param: param, Code: code}, http.StatusBadRequest, ErrOptionWithSkipRetry())
	return err
}
