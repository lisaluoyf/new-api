package controller

import (
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

func TestParameterErrorsStopBeforeFallback(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	for _, code := range []string{"unsupported_reasoning_effort", "unsupported_parameter", "invalid_value", "missing_required_parameter"} {
		err := types.WithOpenAIError(types.OpenAIError{Code: code, Type: "invalid_request_error", Message: "Unsupported request parameter"}, 400)
		decision := evaluateRetry(c, err, 0, 3)
		require.False(t, decision.ShouldRetry)
		require.Equal(t, "invalid_request_parameter", decision.Reason)
	}
	for _, status := range []int{502, 524} {
		err := types.WithOpenAIError(types.OpenAIError{Code: "unknown_error", Message: "Upstream unavailable"}, status)
		require.True(t, evaluateRetry(c, err, 0, 3).ShouldRetry)
	}
}
