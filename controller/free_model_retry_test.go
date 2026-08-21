package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestEvaluateFreeModelRetryRules(t *testing.T) {
	tests := []struct {
		name  string
		err   *types.NewAPIError
		retry bool
	}{
		{"429", types.NewOpenAIError(errors.New("limited"), types.ErrorCodeBadResponseStatusCode, http.StatusTooManyRequests), true},
		{"408", types.NewOpenAIError(errors.New("timeout"), types.ErrorCodeBadResponseStatusCode, http.StatusRequestTimeout), true},
		{"5xx", types.NewOpenAIError(errors.New("upstream"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway), true},
		{"invalid json", types.NewOpenAIError(errors.New("invalid"), types.ErrorCode("invalid_json"), http.StatusBadGateway), true},
		{"400", types.NewOpenAIError(errors.New("bad request"), types.ErrorCodeInvalidRequest, http.StatusBadRequest), false},
		{"401", types.NewOpenAIError(errors.New("auth"), types.ErrorCodeBadResponseStatusCode, http.StatusUnauthorized), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			require.Equal(t, test.retry, evaluateFreeModelRetry(c, test.err, 0, 2).ShouldRetry)
		})
	}
}

func TestEvaluateFreeModelRetryStopsAfterStreamStarted(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(string(constant.ContextKeyIsStream), true)
	c.Writer.WriteHeaderNow()
	decision := evaluateFreeModelRetry(c, types.NewOpenAIError(errors.New("scanner"), types.ErrorCodeBadResponse, http.StatusBadGateway), 0, 2)
	require.False(t, decision.ShouldRetry)
	require.Equal(t, "stream_already_started", decision.Reason)
}
