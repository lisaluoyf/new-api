package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestIOCopyBytesPreservesGatewayRequestID(t *testing.T) {
	for _, key := range []string{common.RequestIdKey, "x-oneapi-request-id", "X-ONEAPI-REQUEST-ID"} {
		t.Run(key, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Set(common.RequestIdKey, "gateway-request")
			// Even if an earlier step changed the response header, the context
			// remains the authoritative ID used by logs and settlement.
			c.Header(common.RequestIdKey, "stale-request")
			payload := []byte(`{"choices":[{"message":{"content":"ok"}}]}`)
			upstream := &http.Response{StatusCode: http.StatusCreated, Header: http.Header{
				key:              {"upstream-request", "second-upstream-request"},
				"Content-Type":   {"application/json"},
				"content-length": {"99999"},
				"Request-Id":     {"provider-trace"},
				"X-Empty":        {},
			}}
			IOCopyBytesGracefully(c, upstream, payload)
			response := recorder.Result()
			defer response.Body.Close()
			require.Equal(t, http.StatusCreated, response.StatusCode)
			require.Equal(t, []string{"gateway-request"}, response.Header.Values(common.RequestIdKey))
			require.Equal(t, "application/json", response.Header.Get("Content-Type"))
			require.Equal(t, "provider-trace", response.Header.Get("Request-Id"))
			require.Equal(t, strconv.Itoa(len(payload)), response.Header.Get("Content-Length"))
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Equal(t, payload, body)
			require.Equal(t, []string{"upstream-request", "second-upstream-request"}, upstream.Header[key])
		})
	}
}

func TestIOCopyBytesDoesNotAdoptUpstreamGatewayID(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	IOCopyBytesGracefully(c, &http.Response{StatusCode: 200, Header: http.Header{common.RequestIdKey: {"upstream-request"}}}, []byte("ok"))
	require.Empty(t, recorder.Result().Header.Get(common.RequestIdKey))
}

func TestIOCopyBytesWithoutUpstreamKeepsGatewayID(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set(common.RequestIdKey, "gateway-request")
	IOCopyBytesGracefully(c, nil, []byte("ok"))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "gateway-request", recorder.Result().Header.Get(common.RequestIdKey))
}
