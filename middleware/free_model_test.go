package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestFreeModelRateLimitSharedByAccount(t *testing.T) {
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedis })
	freeModelMemoryLimiter.Lock()
	freeModelMemoryLimiter.buckets = make(map[string]freeModelMemoryBucket)
	freeModelMemoryLimiter.Unlock()

	allowed, remaining, _, err := consumeFreeModelRateLimit(42, 2)
	require.NoError(t, err)
	require.True(t, allowed)
	require.Equal(t, 1, remaining)
	allowed, remaining, _, err = consumeFreeModelRateLimit(42, 2)
	require.NoError(t, err)
	require.True(t, allowed)
	require.Zero(t, remaining)
	allowed, remaining, _, err = consumeFreeModelRateLimit(42, 2)
	require.NoError(t, err)
	require.False(t, allowed)
	require.Zero(t, remaining)

	allowed, _, _, err = consumeFreeModelRateLimit(43, 2)
	require.NoError(t, err)
	require.True(t, allowed)
}

func TestFreeModelRejectsUnsupportedEndpointBeforeCounting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	freeModelMemoryLimiter.Lock()
	freeModelMemoryLimiter.buckets = make(map[string]freeModelMemoryBucket)
	freeModelMemoryLimiter.Unlock()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"model":"`+service.FreeModelID+`"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	Distribute()(ctx)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "/v1/messages")
	freeModelMemoryLimiter.Lock()
	require.Empty(t, freeModelMemoryLimiter.buckets)
	freeModelMemoryLimiter.Unlock()
}

func TestSupportedFreeModelPaths(t *testing.T) {
	testCases := []struct {
		path    string
		allowed bool
	}{
		{path: "/v1/chat/completions", allowed: true},
		{path: "/v1/responses", allowed: true},
		{path: "/v1/messages", allowed: true},
		{path: "/v1/embeddings", allowed: false},
	}

	for _, tc := range testCases {
		t.Run(tc.path, func(t *testing.T) {
			require.Equal(t, tc.allowed, isSupportedFreeModelPath(tc.path))
		})
	}
}
