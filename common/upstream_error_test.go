package common

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPublicUpstreamErrorPreservesFullUnknownProviderDiagnostics(t *testing.T) {
	message := strings.Repeat("完整的上游错误原因。", 400) + " end-of-error"
	raw, err := Marshal(map[string]any{"error": map[string]any{"code": "FutureProvider.NewError", "message": message, "details": []any{map[string]any{"field": "image[0]", "expected": "image/jpeg", "actual": "video/mp4"}}}, "request_id": "opaque-provider-request"})
	require.NoError(t, err)
	detail := NewPublicUpstreamError(422, raw, http.Header{"X-Request-Id": []string{"opaque-header-request"}})
	require.Equal(t, 422, detail.StatusCode)
	require.Equal(t, "opaque-header-request", detail.RequestID)
	require.False(t, detail.Truncated)
	body := detail.Body.(map[string]any)
	require.Equal(t, message, body["error"].(map[string]any)["message"])
	require.Equal(t, "FutureProvider.NewError", body["error"].(map[string]any)["code"])
}

func TestPublicUpstreamErrorRedactsNestedSecretsAndRequestPayload(t *testing.T) {
	raw := []byte(`{"error":{"code":"InvalidParameter.Format","message":"Expected JPEG; got MP4. Bearer hidden-bearer https://private.example/a?signature=secret known-credential","details":[{"api_key":"hidden-key","SecretAccessKey":"hidden-secret","Authorization":"hidden-auth","request_body":{"prompt":"private-user-prompt"},"session_token":"hidden-session","field":"image[0]","actual":"video/mp4"}]},"headers":{"Cookie":"hidden-cookie"}}`)
	detail := NewPublicUpstreamError(400, raw, nil, "known-credential")
	encoded, err := Marshal(detail)
	require.NoError(t, err)
	for _, secret := range []string{"hidden-bearer", "private.example", "known-credential", "hidden-key", "hidden-secret", "hidden-auth", "private-user-prompt", "hidden-session", "hidden-cookie"} {
		require.NotContains(t, string(encoded), secret)
	}
	require.Contains(t, string(encoded), "Expected JPEG; got MP4")
	require.Contains(t, string(encoded), "video/mp4")
	require.NotContains(t, string(encoded), "truncated")
}

func TestPublicUpstreamErrorBoundsAndReportsOversize(t *testing.T) {
	for _, raw := range [][]byte{[]byte(strings.Repeat("字", 100000)), []byte(strings.Repeat("x", (2<<20)+1))} {
		detail := NewPublicUpstreamError(502, raw, nil)
		encoded, err := Marshal(detail)
		require.NoError(t, err)
		require.True(t, detail.Truncated)
		require.Less(t, len(encoded), MaxPublicUpstreamErrorBytes)
		require.True(t, strings.Contains(string(encoded), "truncated"))
	}
}

func TestFailureDiagnosticsDoesNotPublishTaskRequestOrResult(t *testing.T) {
	raw := []byte(`{"status":"failed","data":{"error":{"code":"NewProviderError","message":"Detailed failure"},"result":{"url":"private-result"}},"request":{"prompt":"private-prompt"},"billing":{"quota":100}}`)
	detail := NewPublicUpstreamError(0, mustMarshalDiagnostics(t, FailureDiagnostics(raw)), nil)
	encoded, err := Marshal(detail)
	require.NoError(t, err)
	require.Contains(t, string(encoded), "Detailed failure")
	require.NotContains(t, string(encoded), "private-result")
	require.NotContains(t, string(encoded), "private-prompt")
	require.NotContains(t, string(encoded), "quota")
}
func mustMarshalDiagnostics(t *testing.T, v any) []byte {
	t.Helper()
	b, err := Marshal(v)
	require.NoError(t, err)
	return b
}
