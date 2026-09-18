package channel

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCancellationObservationUpstreamIDDoesNotLeakAcrossAttempts(t *testing.T) {
	service.InitHttpClient()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/first" {
			w.Header().Set("X-Request-Id", "supplier-first")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	for _, path := range []string{"/first", "/second"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader("{}"))
		req, err := http.NewRequest("POST", srv.URL+path, strings.NewReader("{}"))
		require.NoError(t, err)
		resp, err := doRequest(c, req, info)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		if path == "/first" {
			require.Equal(t, "supplier-first", info.ObservationUpstreamRequestID)
		} else {
			require.Empty(t, info.ObservationUpstreamRequestID)
		}
	}
}
