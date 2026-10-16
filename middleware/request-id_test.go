package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequestIDSurvivesNestedGatewayResponse(t *testing.T) {
	router := gin.New()
	router.Use(RequestId())
	var loggedID string
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		loggedID = c.GetString(common.RequestIdKey)
		require.Equal(t, loggedID, c.Request.Context().Value(common.RequestIdKey))
		service.IOCopyBytesGracefully(c, &http.Response{StatusCode: 200, Header: http.Header{
			"x-oneapi-request-id": {"nested-gateway-id"},
			"Content-Type":        {"application/json"},
		}}, []byte(`{"choices":[]}`))
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	request.Header.Set(common.RequestIdKey, "client-request-id")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.NotEmpty(t, loggedID)
	require.NotEqual(t, "client-request-id", loggedID)
	require.Equal(t, []string{loggedID}, recorder.Result().Header.Values(common.RequestIdKey))
	require.Equal(t, http.StatusOK, recorder.Code)
}
