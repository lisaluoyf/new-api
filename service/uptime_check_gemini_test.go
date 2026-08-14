package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestSendGeminiUptimeProbeUsesNativeEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1beta/models/gemini-3.6-flash:streamGenerateContent", r.URL.Path)
		require.Equal(t, "sse", r.URL.Query().Get("alt"))
		require.Equal(t, "test-key", r.Header.Get("x-goog-api-key"))
		require.Empty(t, r.Header.Get("Authorization"))

		var body struct {
			Contents []struct {
				Role  string `json:"role"`
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"contents"`
			GenerationConfig struct {
				MaxOutputTokens int `json:"maxOutputTokens"`
			} `json:"generationConfig"`
		}
		require.NoError(t, common.DecodeJson(r.Body, &body))
		require.Equal(t, "user", body.Contents[0].Role)
		require.Equal(t, uptimeProbePrompt, body.Contents[0].Parts[0].Text)
		require.Equal(t, uptimeProbeMaxTokens, body.GenerationConfig.MaxOutputTokens)

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hi\"}]}}]}\n\n"))
	}))
	defer server.Close()

	result, probeErr := sendGeminiUptimeProbe(
		context.Background(),
		server.Client(),
		server.URL,
		"test-key",
		"gemini-3.6-flash",
	)
	require.Nil(t, probeErr)
	require.GreaterOrEqual(t, result.LatencyMs, float64(0))
}

func TestBaseURLCandidatesDoNotInventGoogleAPISubdomain(t *testing.T) {
	candidates := baseURLCandidates("https://generativelanguage.googleapis.com")
	for _, candidate := range candidates {
		require.False(t, strings.Contains(candidate, "api.generativelanguage.googleapis.com"), candidate)
	}
}
