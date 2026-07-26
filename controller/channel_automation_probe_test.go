package controller

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestProbeChannelForAutomationUsesClaudeCLIForExclusiveChannel(t *testing.T) {
	var gotPath string
	var gotBody string
	flask := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"latency_ms":1234}`))
	}))
	defer flask.Close()
	t.Setenv("APIMASTER_FLASK_URL", flask.URL)

	baseURL := "https://cc-only.example"
	setting := `{"client_exclusive":"claude_code"}`
	channel := &model.Channel{
		Id:      42,
		BaseURL: &baseURL,
		Key:     "secret-test-key",
		Models:  "claude-fable-5",
		Setting: &setting,
	}

	result, latencyMs := probeChannelForAutomation(channel, "claude-fable-5")

	require.NoError(t, result.localErr)
	require.Nil(t, result.newAPIError)
	require.Equal(t, int64(1234), latencyMs)
	require.Equal(t, "/internal/uptime-probe", gotPath)
	require.Contains(t, gotBody, `"model":"claude-fable-5"`)
	require.Contains(t, gotBody, `"base_url":"https://cc-only.example"`)
	require.Contains(t, gotBody, `"api_format":"claude-cli"`)
}

func TestProbeChannelForAutomationUsesCodexCLIForExclusiveChannel(t *testing.T) {
	var gotPath string
	var gotBody string
	flask := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"latency_ms":2345,"api_format":"codex-cli"}`))
	}))
	defer flask.Close()
	t.Setenv("APIMASTER_FLASK_URL", flask.URL)

	baseURL := "https://codex-only.example"
	setting := `{"client_exclusive":"codex"}`
	channel := &model.Channel{
		Id:      11,
		BaseURL: &baseURL,
		Key:     "secret-test-key",
		Models:  "gpt-5.5",
		Setting: &setting,
	}

	result, latencyMs := probeChannelForAutomation(channel, "gpt-5.5")

	require.NoError(t, result.localErr)
	require.Nil(t, result.newAPIError)
	require.Equal(t, int64(2345), latencyMs)
	require.Equal(t, "/internal/uptime-probe", gotPath)
	require.Contains(t, gotBody, `"model":"gpt-5.5"`)
	require.Contains(t, gotBody, `"base_url":"https://codex-only.example"`)
	require.Contains(t, gotBody, `"api_format":"codex-cli"`)
}
