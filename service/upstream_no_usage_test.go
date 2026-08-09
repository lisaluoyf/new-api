package service

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMarkUpstreamNoUsageSkipsClientGone(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = c.Request.WithContext(ctx)
	MarkUpstreamNoUsage(c, &common.RelayInfo{})
	require.False(t, HasUpstreamNoUsage(c))
}

func TestNewUpstreamNoUsageErrorIsGatewayTimeout(t *testing.T) {
	err := NewUpstreamNoUsageError()
	require.Equal(t, 504, err.StatusCode)
	require.Equal(t, CategoryDisableWindow, ClassifyChannelError(err))
}
