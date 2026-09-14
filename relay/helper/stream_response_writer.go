package helper

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const streamHeartbeat = ": PING\n\n"

// StreamResponseWriter preserves HTTP commitment while separately tracking
// whether fallback would mix business output from two upstream attempts.
type StreamResponseWriter struct {
	gin.ResponseWriter
	mu       sync.Mutex
	output   bool
	ping     bool
	writeErr bool
}

func NewStreamResponseWriter(w gin.ResponseWriter) *StreamResponseWriter {
	return &StreamResponseWriter{ResponseWriter: w, output: w.Written()}
}

func (w *StreamResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *StreamResponseWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	// Only our complete heartbeat is exempt. Unknown or split writes fail closed.
	heartbeat := string(p) == streamHeartbeat
	if len(p) > 0 && !heartbeat {
		w.output = true
	}
	if heartbeat {
		controller := http.NewResponseController(w.ResponseWriter)
		if controller.SetWriteDeadline(time.Now().Add(10*time.Second)) == nil {
			defer controller.SetWriteDeadline(time.Time{})
		}
	}
	n, err := w.ResponseWriter.Write(p)
	if heartbeat && err == nil {
		err = http.NewResponseController(w.ResponseWriter).Flush()
		w.ping = n == len(p)
	}
	if err != nil || n != len(p) {
		w.writeErr = true
	}
	return n, err
}

func (w *StreamResponseWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }

func (w *StreamResponseWriter) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.ResponseWriter.Written() {
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *StreamResponseWriter) WriteHeaderNow() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ResponseWriter.WriteHeaderNow()
}

func (w *StreamResponseWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ResponseWriter.Flush()
}

func (w *StreamResponseWriter) Written() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ResponseWriter.Written()
}

func (w *StreamResponseWriter) Status() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ResponseWriter.Status()
}

func (w *StreamResponseWriter) Size() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ResponseWriter.Size()
}

func (w *StreamResponseWriter) StreamOutputState() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch {
	case w.writeErr:
		return "write_failed"
	case w.output:
		return "business_output"
	case w.ping:
		return "heartbeat_only"
	case w.ResponseWriter.Written():
		return "headers_only"
	default:
		return "not_started"
	}
}

func StreamOutputState(c *gin.Context) string {
	if c == nil || c.Writer == nil {
		return "not_started"
	}
	if w, ok := c.Writer.(interface{ StreamOutputState() string }); ok {
		return w.StreamOutputState()
	}
	if c.Writer.Written() {
		return "business_output"
	}
	return "not_started"
}

func HasStreamBusinessOutput(c *gin.Context) bool {
	state := StreamOutputState(c)
	if state == "headers_only" && c.Writer.Status() != http.StatusOK {
		return true
	}
	return state == "business_output" || state == "write_failed"
}

var _ gin.ResponseWriter = (*StreamResponseWriter)(nil)
