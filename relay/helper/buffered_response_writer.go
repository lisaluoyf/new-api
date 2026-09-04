package helper

import (
	"bufio"
	"bytes"
	"errors"
	"net"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
)

const DefaultBufferedResponseMaxBytes = 8 * 1024 * 1024

type BufferedResponseWriter struct {
	mu          sync.Mutex
	header      http.Header
	status      int
	body        bytes.Buffer
	maxBytes    int
	wroteHeader bool
	overflowed  bool
}

func NewBufferedResponseWriter(maxBytes int) *BufferedResponseWriter {
	if maxBytes <= 0 {
		maxBytes = DefaultBufferedResponseMaxBytes
	}
	return &BufferedResponseWriter{
		header:   make(http.Header),
		status:   http.StatusOK,
		maxBytes: maxBytes,
	}
}

func (w *BufferedResponseWriter) Header() http.Header {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.header
}

func (w *BufferedResponseWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.wroteHeader = true
	if w.body.Len()+len(p) > w.maxBytes {
		w.overflowed = true
		return 0, errors.New("buffered response exceeds max size")
	}
	return w.body.Write(p)
}

func (w *BufferedResponseWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *BufferedResponseWriter) WriteHeader(code int) {
	if code <= 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.wroteHeader {
		return
	}
	w.status = code
	w.wroteHeader = true
}

func (w *BufferedResponseWriter) WriteHeaderNow() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.wroteHeader = true
}

func (w *BufferedResponseWriter) Status() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

func (w *BufferedResponseWriter) Size() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.Len()
}

func (w *BufferedResponseWriter) Written() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.wroteHeader || w.body.Len() > 0
}

func (w *BufferedResponseWriter) Overflowed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.overflowed
}

func (w *BufferedResponseWriter) Snapshot() (http.Header, int, []byte, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.header.Clone(), w.status, append([]byte(nil), w.body.Bytes()...), w.overflowed
}

func (w *BufferedResponseWriter) Flush() {}

func (w *BufferedResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("buffered response writer does not support hijacking")
}

func (w *BufferedResponseWriter) CloseNotify() <-chan bool {
	return make(chan bool)
}

func (w *BufferedResponseWriter) Pusher() http.Pusher {
	return nil
}

func FlushBufferedResponse(dst gin.ResponseWriter, src *BufferedResponseWriter) error {
	if dst == nil || src == nil {
		return nil
	}
	header, status, body, overflowed := src.Snapshot()
	if overflowed {
		return errors.New("buffered response exceeds max size")
	}
	dstHeader := dst.Header()
	for key, values := range header {
		dstHeader.Del(key)
		for _, value := range values {
			dstHeader.Add(key, value)
		}
	}
	if status <= 0 {
		status = http.StatusOK
	}
	dst.WriteHeader(status)
	if len(body) > 0 {
		if _, err := dst.Write(body); err != nil {
			return err
		}
	}
	dst.Flush()
	return nil
}

var _ gin.ResponseWriter = (*BufferedResponseWriter)(nil)
