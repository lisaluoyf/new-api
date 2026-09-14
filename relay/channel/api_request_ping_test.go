package channel

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequestPingerStopsBeforeReturning(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	c.Writer = helper.NewStreamResponseWriter(c.Writer)
	helper.SetEventStreamHeaders(c)
	stop := startPingKeepAlive(c, time.Millisecond)
	require.Eventually(t, func() bool { return c.Writer.Size() > 0 }, time.Second, time.Millisecond)
	stop()
	before := c.Writer.Size()
	time.Sleep(10 * time.Millisecond)
	require.Equal(t, before, c.Writer.Size())
	require.Equal(t, "heartbeat_only", helper.StreamOutputState(c))
}
