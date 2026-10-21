package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestFutureChannelInheritsSharedErrorCapture(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("channel_id", 987654321)
	req := httptest.NewRequest("POST", "http://provider.invalid", nil)
	req.Header.Set("Authorization", "Bearer private-channel-key")
	original := `{"error":{"code":"Future.Error","message":"Precise reason","details":{"actual":"video/mp4","expected":"image/jpeg","key":"private-channel-key"}}}`
	response := &http.Response{StatusCode: 422, Header: http.Header{"X-Request-Id": []string{"opaque-request-12345"}}, Body: io.NopCloser(strings.NewReader(original)), Request: req}
	ObserveUpstreamErrorResponse(c, response, "private-channel-key")
	received, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, original, string(received), "adapter sees the unchanged response")
	failure := types.NewOpenAIError(io.EOF, types.ErrorCodeBadResponseStatusCode, 422)
	AttachCapturedUpstreamError(c, failure)
	require.NotNil(t, failure.UpstreamError)
	require.Equal(t, "opaque-request-12345", failure.UpstreamError.RequestID)
	encoded, err := common.Marshal(failure.ToOpenAIError())
	require.NoError(t, err)
	require.Contains(t, string(encoded), "Precise reason")
	require.NotContains(t, string(encoded), "private-channel-key")
	require.Contains(t, string(encoded), "video/mp4")
	require.NoError(t, response.Body.Close())
	ResetUpstreamErrorCapture(c)
	require.Nil(t, CapturedUpstreamError(c), "another attempt must not inherit a previous channel error")
}

func TestSharedCaptureDoesNotBufferSuccessfulStream(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	original := io.NopCloser(strings.NewReader("stream"))
	response := &http.Response{StatusCode: 200, Body: original}
	ObserveUpstreamErrorResponse(c, response)
	require.Equal(t, original, response.Body)
	require.Nil(t, CapturedUpstreamError(c))
}

func TestRelayErrorHandlerReturnsFullErrorForAnyProvider(t *testing.T) {
	for _, status := range []int{400, 429, 500, 502} {
		message := strings.Repeat("upstream full reason ", 120) + " end-of-message"
		raw, err := common.Marshal(map[string]any{"error": map[string]any{"code": "UnknownFutureProvider.Error", "message": message, "details": map[string]any{"actual": "video/mp4"}}, "request_id": "provider-id"})
		require.NoError(t, err)
		req := httptest.NewRequest("POST", "http://upstream.invalid", nil)
		failure := RelayErrorHandler(req.Context(), &http.Response{StatusCode: status, Header: make(http.Header), Request: req, Body: io.NopCloser(strings.NewReader(string(raw)))}, false)
		require.Equal(t, status, failure.StatusCode)
		require.NotNil(t, failure.UpstreamError)
		api := failure.ToOpenAIError()
		require.Same(t, failure.UpstreamError, api.UpstreamError)
		require.Contains(t, api.UpstreamError.Body.(map[string]any)["error"].(map[string]any)["message"], "end-of-message")
		require.False(t, api.UpstreamError.Truncated)
	}
}

func TestTaskResponseCapturesHTTP200ProviderErrors(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	response := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"Future.AsyncError","message":"precise invalid task response"}}`))}
	ObserveTaskSubmissionResponse(c, response)
	raw, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Contains(t, string(raw), "Future.AsyncError")
	detail := CapturedUpstreamError(c)
	require.NotNil(t, detail)
	require.Equal(t, 200, detail.StatusCode)
	encoded, err := common.Marshal(detail)
	require.NoError(t, err)
	require.Contains(t, string(encoded), "precise invalid task response")
}
