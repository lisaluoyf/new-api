package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const retailOpusExpr = `tier("standard", p * 5 + c * 25 + cr * 0.5 + cc * 6.25 + cc1h * 10) * (param("speed") == "fast" && has(header("anthropic-beta"), "fast-mode-2026-02-01") ? 2 : 1)`
const supplierOpusExpr = `len <= 200000 ? tier("standard", p * 5 + c * 25) : tier("long_context", p * 10 + c * 50)`

func TestChannelRetailBillingOverrideSurvivesRefreshAndPreservesProcurement(t *testing.T) {
	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.ChannelModelPricing{}))
	body, err := common.Marshal(map[string]any{"success": true, "group_ratio": map[string]float64{"pro": 0.5}, "data": []map[string]any{{"model_name": "claude-opus-5", "model_ratio": 2.5, "completion_ratio": 5, "billing_mode": "tiered_expr", "billing_expr": supplierOpusExpr}}})
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
	defer server.Close()
	settingBytes, err := common.Marshal(map[string]any{"key_group": "pro", "model_retail_billing_exprs": map[string]string{"claude-opus-5": retailOpusExpr}})
	require.NoError(t, err)
	setting, baseURL := string(settingBytes), server.URL
	recharge, markup := 0.8, 1.5
	channel := &model.Channel{Id: 15, Setting: &setting, BaseURL: &baseURL, RechargeRate: &recharge, ApimasterPriceRatio: &markup}
	require.NoError(t, db.Create(channel).Error)
	FetchChannelPricing(channel)
	at := time.Now()
	retail, err := ResolveTokenBillingExpression(15, "claude-opus-5", false, at)
	require.NoError(t, err)
	require.Equal(t, "channel_retail_override", retail.Source)
	require.Equal(t, retailOpusExpr, retail.ExprString)
	require.InDelta(t, 0.5*0.8*1.5, *retail.AmountMultiplier, 1e-12)
	supplier, err := ResolveTokenBillingExpression(15, "claude-opus-5", true, at)
	require.NoError(t, err)
	require.Equal(t, supplierOpusExpr, supplier.ExprString)
	require.InDelta(t, 0.5*0.8, *supplier.AmountMultiplier, 1e-12)
	for _, length := range []float64{199999, 200000, 200001, 900000, 1000000} {
		for _, request := range []struct {
			body, header string
			factor       float64
		}{
			{`{}`, "", 1}, {`{}`, "fast-mode-2026-02-01", 1},
			{`{"speed":"standard"}`, "fast-mode-2026-02-01", 1},
			{`{"speed":"fast"}`, "", 1},
			{`{"speed":"fast"}`, "other-beta,fast-mode-2026-02-01", 2},
		} {
			cost, trace, err := billingexpr.RunExprWithRequest(retail.ExprString, billingexpr.TokenParams{P: 1000, C: 100, CR: length - 1000, Len: length, CC: 100, CC1h: 100}, billingexpr.RequestInput{Headers: map[string]string{"anthropic-beta": request.header}, Body: []byte(request.body)})
			require.NoError(t, err)
			require.Equal(t, "standard", trace.MatchedTier)
			require.InDelta(t, (1000*5+100*25+(length-1000)*0.5+100*6.25+100*10)*request.factor, cost, 1e-8)
		}
	}
	// A new supplier refresh changes procurement while the frozen retail policy
	// remains effective for new requests and leaves in-flight snapshots intact.
	FetchChannelPricing(channel)
	again, err := ResolveTokenBillingExpression(15, "claude-opus-5", false, at)
	require.NoError(t, err)
	require.Equal(t, retail.ExprString, again.ExprString)
	require.Equal(t, *retail.AmountMultiplier, *again.AmountMultiplier)
	require.NoError(t, db.Model(channel).Update("setting", `{"key_group":"pro"}`).Error)
	noOverride, err := ResolveTokenBillingExpression(15, "claude-opus-5", false, at)
	require.NoError(t, err)
	require.Equal(t, "channel_expression", noOverride.Source)
	require.Equal(t, supplierOpusExpr, noOverride.ExprString)
	require.Equal(t, retailOpusExpr, retail.ExprString)
}

func TestChannelRetailBillingOverrideRejectsInvalidAndIgnoresOtherModels(t *testing.T) {
	for _, expression := range []string{"", `tier("bad", p ***)`, `fixed(1)`} {
		raw, err := common.Marshal(map[string]any{"model_retail_billing_exprs": map[string]string{"claude-opus-5": expression}})
		require.NoError(t, err)
		setting := string(raw)
		_, err = channelRetailBillingExpression(&setting, "claude-opus-5")
		require.Error(t, err)
		other, err := channelRetailBillingExpression(&setting, "gpt-6-sol")
		require.NoError(t, err)
		require.Empty(t, other)
	}
}
