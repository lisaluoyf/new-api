package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEmbeddingUptimeUsesMappedEmbeddingEndpoint(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.ChannelDetectLog{}))
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/embeddings", r.URL.Path)
		require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		var payload map[string]any
		require.NoError(t, common.DecodeJson(r.Body, &payload))
		require.Equal(t, "baai/bge-m3", payload["model"])
		require.Equal(t, "float", payload["encoding_format"])
		require.Equal(t, "hi", payload["input"])
		require.NotContains(t, payload, "messages")
		require.NotContains(t, payload, "stream")
		_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[0.1,-0.2,0.3]}]}`))
	}))
	defer server.Close()
	baseURL, mapping := server.URL+"/api", `{"bge-m3":"baai/bge-m3"}`
	probeOneChannel(context.Background(), &model.Channel{Id: 38, Key: "test-key", BaseURL: &baseURL, ModelMapping: &mapping}, "bge-m3")
	var log model.ChannelDetectLog
	require.NoError(t, db.First(&log).Error)
	require.Equal(t, "uptime", log.Source)
	require.Equal(t, "bge-m3", log.ClaimedModel)
	require.Equal(t, "pass", log.Status)
}

func TestEmbeddingUptimeRejectsFalseSuccess(t *testing.T) {
	for _, body := range []string{`{"error":{"message":"unavailable"}}`, `{"data":[]}`, `{"data":[{"embedding":[]}]}`, `{"data":[{"embedding":[0,0]}]}`, `{"data":[{"embedding":"base64"}]}`, `<html>blocked</html>`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			_, err := sendEmbeddingUptimeProbe(context.Background(), server.Client(), server.URL+"/v1", "test-key", "text-embedding-3-small")
			require.NotNil(t, err)
		})
	}
}
