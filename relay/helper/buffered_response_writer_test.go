package helper

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestBufferedResponseWriterFlushesSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	buffer := NewBufferedResponseWriter(1024)
	buffer.Header().Set("Content-Type", "application/json")
	buffer.Header().Add("X-Test", "one")
	buffer.WriteHeader(http.StatusCreated)
	if _, err := buffer.Write([]byte(`{"ok":true}`)); err != nil {
		t.Fatalf("write buffered response: %v", err)
	}

	if err := FlushBufferedResponse(c.Writer, buffer); err != nil {
		t.Fatalf("flush buffered response: %v", err)
	}

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusCreated)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type = %q", got)
	}
	if got := recorder.Body.String(); got != `{"ok":true}` {
		t.Fatalf("body = %q", got)
	}
}

func TestBufferedResponseWriterOverflow(t *testing.T) {
	buffer := NewBufferedResponseWriter(3)

	if _, err := buffer.Write([]byte("abcd")); err == nil {
		t.Fatal("expected overflow error")
	}
	if !buffer.Overflowed() {
		t.Fatal("expected overflow flag")
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	if err := FlushBufferedResponse(c.Writer, buffer); err == nil {
		t.Fatal("expected flush overflow error")
	}
}
