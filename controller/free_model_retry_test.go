package controller

import (
	"context"
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
		{"500", types.NewOpenAIError(errors.New("upstream"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError), true},
		{"502", types.NewOpenAIError(errors.New("upstream"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway), true},
		{"503", types.NewOpenAIError(errors.New("upstream"), types.ErrorCodeBadResponseStatusCode, http.StatusServiceUnavailable), true},
		{"504", types.NewOpenAIError(errors.New("upstream"), types.ErrorCodeBadResponseStatusCode, http.StatusGatewayTimeout), true},
		{"524", types.NewOpenAIError(errors.New("upstream"), types.ErrorCodeBadResponseStatusCode, 524), true},
		{"network", types.NewOpenAIError(errors.New("network"), types.ErrorCodeDoRequestFailed, 0), true},
		{"404 endpoint", types.NewOpenAIError(errors.New("missing endpoint"), types.ErrorCode("unknown_error"), http.StatusNotFound), true},
		{"model not found", types.NewOpenAIError(errors.New("missing model"), types.ErrorCodeModelNotFound, http.StatusBadRequest), true},
		{"200 error object", types.NewOpenAIError(errors.New("upstream error object"), types.ErrorCode("502"), http.StatusOK), true},
		{"invalid json", types.NewOpenAIError(errors.New("invalid"), types.ErrorCode("invalid_json"), http.StatusBadGateway), true},
		{"schema mismatch", types.NewOpenAIError(errors.New("schema"), types.ErrorCode("schema_validation_failed"), http.StatusBadGateway), true},
		{"400", types.NewOpenAIError(errors.New("bad request"), types.ErrorCodeInvalidRequest, http.StatusBadRequest), false},
		{"401", types.NewOpenAIError(errors.New("auth"), types.ErrorCodeBadResponseStatusCode, http.StatusUnauthorized), false},
		{"403", types.NewOpenAIError(errors.New("permission"), types.ErrorCodeBadResponseStatusCode, http.StatusForbidden), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			require.Equal(t, test.retry, evaluateFreeModelRetry(c, test.err, 0, 2).ShouldRetry)
		})
	}
}

func TestEvaluateFreeModelRetryNormalizesErrorObjectSuccessStatus(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	err := types.NewOpenAIError(errors.New("upstream error object"), types.ErrorCode("502"), http.StatusOK)
	decision := evaluateFreeModelRetry(c, err, 0, 0)
	require.False(t, decision.ShouldRetry)
	require.Equal(t, http.StatusBadGateway, decision.StatusCode)
	require.Equal(t, http.StatusBadGateway, err.StatusCode)
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

func TestEvaluateFreeModelRetryAllowsFallbackBeforeStreamStarts(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(string(constant.ContextKeyIsStream), true)
	decision := evaluateFreeModelRetry(c, types.NewOpenAIError(errors.New("first byte timeout"), types.ErrorCodeBadResponse, http.StatusGatewayTimeout), 0, 2)
	require.True(t, decision.ShouldRetry)
}

func TestEvaluateFreeModelRetryStopsAfterClientCancellation(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx, cancel := context.WithCancel(request.Context())
	c.Request = request.WithContext(ctx)
	cancel()
	decision := evaluateFreeModelRetry(c, types.NewOpenAIError(errors.New("canceled"), types.ErrorCodeDoRequestFailed, 0), 0, 2)
	require.False(t, decision.ShouldRetry)
	require.Equal(t, "client_canceled", decision.Reason)
}
