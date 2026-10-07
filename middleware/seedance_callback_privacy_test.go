package middleware

import (
	"bytes"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"net/http/httputil"
	"testing"
)

func TestSeedanceCallbackCredentialsDoNotReachRequestDumps(t *testing.T) {
	var log bytes.Buffer
	old := gin.DefaultWriter
	gin.DefaultWriter = &log
	t.Cleanup(func() { gin.DefaultWriter = old })
	router := gin.New()
	router.Use(ProtectSeedanceSensitiveRequest())
	SetUpLogger(router)
	router.GET("/v1/seedance2/private-avatar/callback/:id/:state", func(c *gin.Context) {
		require.Equal(t, "private-byted-token", c.GetString("seedance_callback_token"))
		require.Empty(t, c.Request.URL.RawQuery)
		dump, e := httputil.DumpRequest(c.Request, false)
		require.NoError(t, e)
		require.NotContains(t, string(dump), "private-byted-token")
		require.NotContains(t, string(dump), "secret-state")
		c.Status(200)
	})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/v1/seedance2/private-avatar/callback/id/secret-state?bytedToken=private-byted-token&resultCode=10000", nil))
	require.Equal(t, 200, w.Code)
	require.NotContains(t, log.String(), "private-byted-token")
	require.NotContains(t, log.String(), "secret-state")
}
