package controller

import (
	"bytes"
	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"net/http"
)

// With notifications, do not publish an accepted task before its durable mapping exists.
type taskWebhookResponseWriter struct {
	gin.ResponseWriter
	body bytes.Buffer
	code int
}

func (w *taskWebhookResponseWriter) WriteHeader(code int) { w.code = code }
func (w *taskWebhookResponseWriter) WriteHeaderNow()      {}
func (w *taskWebhookResponseWriter) Write(body []byte) (int, error) {
	if w.code == 0 {
		w.code = 200
	}
	return w.body.Write(body)
}
func (w *taskWebhookResponseWriter) WriteString(body string) (int, error) {
	return w.Write([]byte(body))
}
func (w *taskWebhookResponseWriter) Status() int {
	if w.code == 0 {
		return 200
	}
	return w.code
}
func (w *taskWebhookResponseWriter) Size() int     { return w.body.Len() }
func (w *taskWebhookResponseWriter) Written() bool { return w.code != 0 }
func (w *taskWebhookResponseWriter) Flush()        {}
func (w *taskWebhookResponseWriter) failPersistence() {
	w.body.Reset()
	w.code = http.StatusServiceUnavailable
	body, _ := common.Marshal(gin.H{"error": gin.H{"code": "task_persistence_failed", "message": "Task submission outcome could not be persisted. Do not resubmit automatically; contact support with the request ID."}})
	_, _ = w.body.Write(body)
}
func (w *taskWebhookResponseWriter) commit(c *gin.Context) {
	c.Writer = w.ResponseWriter
	if w.code != 0 {
		w.ResponseWriter.WriteHeader(w.code)
		_, _ = w.ResponseWriter.Write(w.body.Bytes())
	}
}
