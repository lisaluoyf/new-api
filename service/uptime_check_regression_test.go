package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProbeModelMappingCaseVariants(t *testing.T) {
	for _, tc := range []struct{ mapping, input, want string }{
		{`{"MiniMax-M3.1-Flash":"minimax/minimax-m3.1-flash:free"}`, "minimax-m3.1-flash", "minimax/minimax-m3.1-flash:free"},
		{`{"MiniMax-M3":"MiniMax/MiniMax-M3"}`, "minimax-m3", "MiniMax/MiniMax-M3"},
		{`{"Model":"exact","model":"other"}`, "Model", "exact"},
		{`{"Model":"exact","model":"other"}`, "MODEL", ""},
		{`{"Model":"Intermediate","intermediate":"provider/model"}`, "model", "provider/model"},
		{`{"Model":"model"}`, "model", ""},
	} {
		t.Run(tc.input+tc.mapping, func(t *testing.T) { require.Equal(t, tc.want, ModelMappingTarget(&tc.mapping, tc.input)) })
	}
}

func TestUptime404Classification(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		kind       probeErrKind
	}{
		{"OpenRouter numeric code", `{"error":{"code":404,"message":"No endpoints found for minimax-m3.1-flash."}}`, probeErrModel},
		{"model code", `{"error":{"code":"model_not_found","message":"Unknown model"}}`, probeErrModel},
		{"JSON provider failure", `{"error":{"code":404,"message":"Provider unavailable"}}`, probeErrOther},
		{"HTML route", `<html>Route not found</html>`, probeErrURL},
		{"plain route", `404 page not found`, probeErrURL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404); fmt.Fprint(w, tc.body) }))
			defer srv.Close()
			_, err := sendUptimeProbe(context.Background(), srv.Client(), srv.URL, "test-key", "minimax-m3.1-flash", nil)
			require.NotNil(t, err)
			require.Equal(t, tc.kind, err.kind)
			require.Contains(t, err.msg, "404")
			if tc.name == "OpenRouter numeric code" {
				require.Contains(t, err.msg, "No endpoints found")
			}
		})
	}
}

func TestUptimeURLCandidatesKeepConfiguredHost(t *testing.T) {
	for _, base := range []string{"https://openrouter.ai/api", "https://api.api-hub.asia/v1", "https://www.blackaicoding.com", "https://generativelanguage.googleapis.com"} {
		expected, err := url.Parse(base)
		require.NoError(t, err)
		candidates := baseURLCandidates(base)
		require.Equal(t, base, candidates[0])
		for _, candidate := range candidates {
			u, err := url.Parse(candidate)
			require.NoError(t, err)
			require.Equal(t, expected.Host, u.Host)
		}
	}
}

func TestUptimeProbeCaseMappingAndModel404DoNotRetryPaths(t *testing.T) {
	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.ChannelDetectLog{}))
	for _, success := range []bool{true, false} {
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			require.Equal(t, "/api/v1/chat/completions", r.URL.Path)
			var body struct {
				Model string `json:"model"`
			}
			require.NoError(t, common.DecodeJson(r.Body, &body))
			require.Equal(t, "minimax/minimax-m3.1-flash:free", body.Model)
			if !success {
				w.WriteHeader(404)
				fmt.Fprint(w, `{"error":{"code":404,"message":"No endpoints found for minimax/minimax-m3.1-flash:free."}}`)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		}))
		base := srv.URL + "/api"
		mapping := `{"MiniMax-M3.1-Flash":"minimax/minimax-m3.1-flash:free"}`
		probeOneChannel(context.Background(), &model.Channel{Id: 38, BaseURL: &base, Key: "test-key", ModelMapping: &mapping}, "minimax-m3.1-flash")
		srv.Close()
		require.Equal(t, 1, calls)
		var result model.ChannelDetectLog
		require.NoError(t, db.Order("id DESC").First(&result).Error)
		require.Equal(t, "minimax-m3.1-flash", result.ClaimedModel)
		if success {
			require.Equal(t, "pass", result.Status)
		} else {
			require.Equal(t, "notcomplete", result.Status)
			require.Contains(t, result.Note, "No endpoints found")
		}
	}
}
