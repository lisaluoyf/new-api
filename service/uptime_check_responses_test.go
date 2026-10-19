package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestResponsesUptimeUsesConfiguredProtocolAndMappedModel(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.ChannelDetectLog{}))
	previousDB := model.DB
	model.DB = db
	settings := model_setting.GetGlobalSettings()
	previousPolicy := settings.ChatCompletionsToResponsesPolicy
	t.Cleanup(func() { model.DB = previousDB; settings.ChatCompletionsToResponsesPolicy = previousPolicy })
	settings.ChatCompletionsToResponsesPolicy = model_setting.ChatCompletionsToResponsesPolicy{Enabled: true, ChannelIDs: []int{79}, ModelPatterns: []string{`^grok-4[.][567]$`}}
	for _, tc := range []struct {
		id        int
		responses bool
	}{{79, true}, {80, false}} {
		t.Run(string(rune(tc.id)), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
				var body map[string]any
				require.NoError(t, common.DecodeJson(r.Body, &body))
				require.Equal(t, "grok-native", body["model"])
				w.Header().Set("Content-Type", "text/event-stream")
				if tc.responses {
					require.Equal(t, "/api/v1/responses", r.URL.Path)
					require.Equal(t, "hi", body["input"])
					require.Equal(t, true, body["stream"])
					require.Equal(t, false, body["store"])
					require.NotContains(t, body, "messages")
					require.NotContains(t, body, "max_tokens")
					_, _ = w.Write([]byte("data: {\"type\":\"response.created\"}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n"))
				} else {
					require.Equal(t, "/api/v1/chat/completions", r.URL.Path)
					require.Contains(t, body, "messages")
					_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
				}
			}))
			defer server.Close()
			baseURL, mapping := server.URL+"/api", `{"grok-4.5":"grok-native"}`
			probeOneChannel(context.Background(), &model.Channel{Id: tc.id, Type: constant.ChannelTypeOpenAI, Key: "test-key", BaseURL: &baseURL, ModelMapping: &mapping}, "grok-4.5")
			var row model.ChannelDetectLog
			require.NoError(t, db.Where("channel_id = ?", tc.id).First(&row).Error)
			require.Equal(t, "pass", row.Status)
			require.Equal(t, "uptime", row.Source)
			require.Equal(t, "grok-4.5", row.ClaimedModel)
			require.Equal(t, 1, calls)
		})
	}
}

func TestResponsesUptimeRejectsFalseSuccess(t *testing.T) {
	for _, body := range []string{
		"data: {\"type\":\"response.created\"}\n\n",
		"data: {\"type\":\"response.created\"}\n\ndata: {\"type\":\"response.failed\"}\n\n",
		"data: {\"type\":\"error\",\"error\":{\"message\":\"failed\"}}\n\n",
		"data: {\"type\":\"response.incomplete\"}\n\n",
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\" \"}\n\n",
		"data: {\"type\":\"response.completed\"}\n\n",
		"<html>blocked</html>", "data: invalid\n\n", "",
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			_, err := sendResponsesUptimeProbe(context.Background(), server.Client(), server.URL, "test-key", "grok-4.5", nil)
			require.NotNil(t, err)
		})
	}
}

func TestResponsesUptimeMeasuresOutputNotMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.created\"}\n\n"))
		w.(http.Flusher).Flush()
		time.Sleep(25 * time.Millisecond)
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n"))
	}))
	defer server.Close()
	result, err := sendResponsesUptimeProbe(context.Background(), server.Client(), server.URL, "test-key", "grok-4.5", nil)
	require.Nil(t, err)
	require.GreaterOrEqual(t, result.LatencyMs, float64(20))
}

func TestResponsesUptimePreservesModelErrorsAndRedactsKeys(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"model_not_found","message":"model not found: test-key"}}`))
	}))
	defer server.Close()
	_, err := sendResponsesUptimeProbe(context.Background(), server.Client(), server.URL, "test-key", "grok-4.5", nil)
	require.NotNil(t, err)
	require.Equal(t, probeErrModel, err.kind)
	require.NotContains(t, err.msg, "test-key")
	require.Contains(t, err.msg, "model not found")
}
