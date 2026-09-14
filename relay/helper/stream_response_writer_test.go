package helper

import (
	"errors"
	"io"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestStreamResponseWriterConcurrentHeartbeatAndOutput(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	c.Writer = NewStreamResponseWriter(c.Writer)
	SetEventStreamHeaders(c)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_, _ = c.Writer.WriteString("data: {}\n\n")
				_ = PingData(c)
				_ = c.Writer.Written()
				_ = c.Writer.Size()
				_ = c.Writer.Status()
				_ = StreamOutputState(c)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, "business_output", StreamOutputState(c))
}

func TestStreamResponseWriterHedgeHeartbeat(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	tracked := NewStreamResponseWriter(c.Writer)
	c.Writer = tracked
	gate := NewStreamGate(NewSharedClientSink(tracked), func(*StreamGate) bool { return true })
	_, err := gate.Write([]byte(streamHeartbeat))
	require.NoError(t, err)
	require.Equal(t, "heartbeat_only", StreamOutputState(c))
	require.False(t, gate.IsLive())
	_, err = gate.WriteString("event: response.created\n")
	require.NoError(t, err)
	require.True(t, HasStreamBusinessOutput(c))
}

type failingStreamWriter struct{ gin.ResponseWriter }

func (w failingStreamWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestStreamResponseWriterFailedHeartbeatBlocksRetry(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Writer = NewStreamResponseWriter(failingStreamWriter{c.Writer})
	err := PingData(c)
	require.True(t, errors.Is(err, io.ErrClosedPipe))
	require.Equal(t, "write_failed", StreamOutputState(c))
	require.True(t, HasStreamBusinessOutput(c))
}
