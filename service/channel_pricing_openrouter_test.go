package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestParseOpenRouterModelPrices(t *testing.T) {
	body := []byte(`{
		"data": [
			{
				"id": "anthropic/claude-fable-5",
				"pricing": {
					"prompt": "0.00001",
					"completion": "0.00005",
					"input_cache_read": "0.000001",
					"input_cache_write": "0.0000125"
				}
			},
			{
				"id": "free-model",
				"pricing": {"prompt": "0", "completion": "0"}
			}
		]
	}`)
	prices, err := parseOpenRouterModelPrices(body)
	require.NoError(t, err)
	require.InDelta(t, 10.0, prices["anthropic/claude-fable-5"].InputPrice, 0.0001)
	require.InDelta(t, 50.0, prices["anthropic/claude-fable-5"].OutputPrice, 0.0001)
	require.InDelta(t, 1.0, prices["anthropic/claude-fable-5"].CachePrice, 0.0001)
	require.InDelta(t, 12.5, prices["anthropic/claude-fable-5"].CacheCreationPrice, 0.0001)
	require.InDelta(t, 0.0, prices["free-model"].InputPrice, 0.0001)
}

func TestOpenRouterEmbeddingPricesUseSeparateCatalog(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.ChannelModelPricing{}))
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/models":
			_, _ = w.Write([]byte(`{"data":[]}`))
		case "/api/v1/embeddings/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"openai/text-embedding-3-small","pricing":{"prompt":"0.00000002","completion":"0"}},{"id":"baai/bge-m3","pricing":{"prompt":"0.00000001","completion":"0"}}]}`))
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	baseURL, mapping := server.URL+"/api", `{"text-embedding-3-small":"openai/text-embedding-3-small","bge-m3":"baai/bge-m3"}`
	require.True(t, fetchOpenRouterChannelPricing(context.Background(), &model.Channel{Id: 38, Type: constant.ChannelTypeOpenRouter, BaseURL: &baseURL, Key: "test-key", Models: "text-embedding-3-small,bge-m3", ModelMapping: &mapping}))
	var rows []model.ChannelModelPricing
	require.NoError(t, db.Order("model_name").Find(&rows).Error)
	require.Len(t, rows, 2)
	require.InDelta(t, 0.01, rows[0].InputPrice, 1e-10)
	require.InDelta(t, 0.02, rows[1].InputPrice, 1e-10)
	for _, row := range rows {
		require.Zero(t, row.OutputPrice)
		require.Equal(t, "api", row.PricingSource)
	}
}

func TestChannelModelsFromList(t *testing.T) {
	require.Equal(t, []string{"claude-fable-5", "claude-sonnet-4-6"},
		channelModelsFromList("claude-fable-5, claude-sonnet-4-6"))
}
