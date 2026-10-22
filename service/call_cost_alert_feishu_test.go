package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestCallCostWebhookRequiresDedicatedConfiguration(t *testing.T) {
	t.Setenv("FEISHU_ALERT_WEBHOOK_URL", "https://open.feishu.cn/open-apis/bot/v2/hook/wrong-env-group")
	t.Setenv("FEISHU_OPS_CHAT_ID", "wrong-ops-group")
	for _, invalid := range []string{"", "https://example.org/hook/secret", "http://open.feishu.cn/open-apis/bot/v2/hook/secret", "https://open.feishu.cn/open-apis/bot/v2/hook/"} {
		t.Setenv("CALL_COST_ALERT_WEBHOOK_URL", invalid)
		_, err := callCostAlertWebhook()
		require.Error(t, err)
		require.NotContains(t, err.Error(), "secret")
	}
	want := "https://open.feishu.cn/open-apis/bot/v2/hook/test-security-group"
	t.Setenv("CALL_COST_ALERT_WEBHOOK_URL", want)
	got, err := callCostAlertWebhook()
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestCallCostWebhookResponseValidation(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		status         int
		success        bool
	}{
		{"current", `{"code":0}`, 200, true},
		{"legacy", `{"StatusCode":0}`, 200, true},
		{"reject", `{"code":19024}`, 200, false},
		{"legacy reject", `{"StatusCode":1}`, 200, false},
		{"conflicting", `{"code":0,"StatusCode":1}`, 200, false},
		{"missing", `{}`, 200, false},
		{"null", `{"code":null}`, 200, false},
		{"invalid", `not-json`, 200, false},
		{"http error", `{"code":0}`, 503, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				var payload map[string]any
				require.NoError(t, common.DecodeJson(r.Body, &payload))
				require.Equal(t, "interactive", payload["msg_type"])
				require.NotNil(t, payload["card"])
				require.Empty(t, r.Header.Get("Authorization"))
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.response)
			}))
			defer server.Close()
			err := deliverCallCostAlert(context.Background(), server.Client(), server.URL, map[string]any{"schema": "2.0"})
			if tc.success {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

type callCostWebhookTransport func(*http.Request) (*http.Response, error)

func (f callCostWebhookTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCallCostWebhookCancellationRedactsSecret(t *testing.T) {
	client := &http.Client{Transport: callCostWebhookTransport(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, fmt.Errorf("request to %s: %w", r.URL, r.Context().Err())
	})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := deliverCallCostAlert(ctx, client, "https://open.feishu.cn/open-apis/bot/v2/hook/private-secret", nil)
	require.Error(t, err)
	require.False(t, strings.Contains(err.Error(), "private-secret"))
}
