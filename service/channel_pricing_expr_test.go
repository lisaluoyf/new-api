package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFlatTieredPricing(t *testing.T) {
	prices, ok := flatTieredPricing("tiered_expr", `tier("common", p * 0.2 + c * 1.2 + cr * 0.02 + cc * 0.25)`)
	require.True(t, ok)
	require.InDelta(t, 0.2, prices.InputPrice, 0.0000001)
	require.InDelta(t, 1.2, prices.OutputPrice, 0.0000001)
	require.InDelta(t, 0.02, prices.CachePrice, 0.0000001)
	require.InDelta(t, 0.25, prices.CacheCreationPrice, 0.0000001)
}

func TestFlatTieredPricingRejectsConditionalExpression(t *testing.T) {
	_, ok := flatTieredPricing("tiered_expr", `len <= 200000 ? tier("short", p * 1) : tier("long", p * 2)`)
	require.False(t, ok)
	_, ok = flatTieredPricing("tiered_expr", `tier("conditional", p > 100 ? p * 1 : p * 2) + c * 3`)
	require.False(t, ok)
}

func TestFetchChannelPricingPrefersFlatTieredExpression(t *testing.T) {
	response := map[string]any{
		"success":     true,
		"group_ratio": map[string]float64{"pro": 0.3},
		"data": []map[string]any{
			{
				"model_name":         "gpt-5.6-luna",
				"quota_type":         0,
				"model_ratio":        0.5,
				"completion_ratio":   6,
				"cache_ratio":        0.1,
				"create_cache_ratio": 1.25,
				"billing_mode":       "tiered_expr",
				"billing_expr":       `tier("common", p * 0.2 + c * 1.2 + cr * 0.02 + cc * 0.25)`,
			},
			{
				"model_name":         "gpt-5.6-sol",
				"quota_type":         0,
				"model_ratio":        2.5,
				"completion_ratio":   8,
				"cache_ratio":        0.1,
				"create_cache_ratio": 1.25,
				"billing_mode":       "tiered_expr",
				"billing_expr":       `tier("common", p * 5 + c * 30 + cr * 0.5 + cc * 6.25)`,
			},
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/pricing", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(response))
	}))
	defer server.Close()

	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.ChannelModelPricing{}))

	setting := `{"key_group":"pro"}`
	baseURL := server.URL
	channel := &model.Channel{Id: 52, BaseURL: &baseURL, Setting: &setting}
	FetchChannelPricing(channel)

	row, err := model.GetChannelModelPricing(52, "gpt-5.6-luna")
	require.NoError(t, err)
	require.NotNil(t, row)
	require.InDelta(t, 0.06, row.InputPrice, 0.0000001)
	require.InDelta(t, 0.36, row.OutputPrice, 0.0000001)
	require.InDelta(t, 0.006, row.CachePrice, 0.0000001)
	require.InDelta(t, 0.075, row.CacheCreationPrice, 0.0000001)
	require.InDelta(t, 0.3, row.GroupRatio, 0.0000001)
	require.Equal(t, "tiered_expr", row.BillingMode)
	require.Contains(t, row.BillingExpr, "p * 0.2")

	sol, err := model.GetChannelModelPricing(52, "gpt-5.6-sol")
	require.NoError(t, err)
	require.NotNil(t, sol)
	require.InDelta(t, 1.5, sol.InputPrice, 0.0000001)
	require.InDelta(t, 9.0, sol.OutputPrice, 0.0000001)
	require.InDelta(t, 0.15, sol.CachePrice, 0.0000001)
	require.InDelta(t, 1.875, sol.CacheCreationPrice, 0.0000001)
	require.Contains(t, sol.BillingExpr, "p * 5")
}
