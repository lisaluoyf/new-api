package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestSeedanceSubmitStructuredErrors(t *testing.T) {
	for _, status := range []int{400, 422} {
		err := PublicVideoSubmitError(status, []byte(`{"error":{"code":"InputImageSensitiveContentDetected","message":"The input image may contain sensitive information. Bearer private-key https://private.example/image.png"}}`), "private-key", 0)
		require.Equal(t, "InputImageSensitiveContentDetected", err.Code)
		require.Equal(t, status, err.StatusCode)
		require.True(t, err.LocalError)
		require.Contains(t, err.Message, "input image may contain sensitive information")
		require.NotContains(t, err.Message, "private-key")
		require.NotContains(t, err.Message, "private.example")
	}
	for _, status := range []int{401, 403, 500, 502, 503} {
		err := PublicVideoSubmitError(status, []byte(`{"error":{"code":"Unauthorized","message":"Authorization: Bearer private-key; account_id=hidden"}}`), "private-key", 0)
		require.Equal(t, "video_service_error", err.Code)
		require.Equal(t, "Video service request failed", err.Message)
		require.False(t, err.LocalError, "Infrastructure errors must remain eligible for existing fallback rules")
	}
	err := PublicVideoSubmitError(429, []byte(`{"error":"secret"}`), "", 0)
	require.Equal(t, "rate_limit_exceeded", err.Code)
	require.False(t, err.LocalError)
	err = PublicVideoSubmitError(429, []byte(`{"error":{"code":"QuotaExceeded","message":"The request has exceeded the quota; retry later."}}`), "", 0)
	require.Equal(t, "QuotaExceeded", err.Code)
	require.Contains(t, err.Message, "exceeded the quota")
	require.False(t, err.LocalError)
	err = PublicVideoSubmitError(400, []byte(`{malformed`), "", 0)
	require.Equal(t, "invalid_request", err.Code)
	require.Equal(t, "Invalid video generation request", err.Message)
}

func TestSeedanceCompatibleQueryKeepsSpecificCode(t *testing.T) {
	for _, name := range []string{"seedance-2.0", "seedance-2.5", "seedance-2.0-mini", "seedance-2.0-fast"} {
		task := &model.Task{Status: model.TaskStatusFailure, Properties: model.Properties{OriginModelName: name},
			Data: []byte(`{"error":{"code":"OutputVideoSensitiveContentDetected","message":"The output video may contain sensitive information."}}`)}
		public := PublicTaskError(task)
		response := SeedanceCompatibleTaskResponse(task)
		require.Equal(t, public, response["data"].(map[string]any)["error"])
		require.Equal(t, "OutputVideoSensitiveContentDetected", public.Code)
	}
}
