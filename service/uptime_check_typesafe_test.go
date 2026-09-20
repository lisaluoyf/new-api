package service

import (
	"context"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTypeSafeUptimeUsesNativeProtocol(t *testing.T) {
	bad := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/systemone", r.URL.Path)
		require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		var request dto.TypeSafeRequest
		require.NoError(t, common.DecodeJson(r.Body, &request))
		require.NoError(t, request.Validate())
		require.Equal(t, "jev-latest", request.Model)
		w.Header().Set("Content-Type", "application/json")
		if bad {
			_, _ = w.Write([]byte(`{"error":"overloaded"}`))
			return
		}
		_, _ = w.Write([]byte(`{"model":"jev-1.13.0","answers":{"healthy":{"type":"noul","noul":1}},"usage":{"input_tokens":100,"output_tokens":10}}`))
	}))
	defer srv.Close()
	_, err := sendTypeSafeUptimeProbe(context.Background(), srv.Client(), srv.URL, "test-key", "jev-latest")
	require.Nil(t, err)
	bad = true
	_, err = sendTypeSafeUptimeProbe(context.Background(), srv.Client(), srv.URL, "test-key", "jev-latest")
	require.NotNil(t, err)
}
