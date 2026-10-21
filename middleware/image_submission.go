package middleware

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func ImageSubmissionRecoveryAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		recovery := c.Request.Method == http.MethodGet && (c.Request.URL.Path == "/v1/image-submissions/by-key" || c.Request.URL.Path == "/v1/image-submissions/by-key/response")
		recovery = recovery || (service.IsImageSubmissionRequest(c) && c.GetHeader("Idempotency-Key") != "")
		if !recovery {
			c.Next()
			return
		}
		tokenAuth(true)(c)
	}
}

// Installed before quota checks, webhook preparation, distribution and handlers.
// A replay returns the receipt without entering any generation or billing code.
func DurableImageSubmission() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.GetHeader("Idempotency-Key")
		if !service.IsImageSubmissionRequest(c) || key == "" {
			c.Next()
			return
		}
		if !service.ValidVideoSubmissionKey(key) {
			submissionError(c, 400, "invalid_idempotency_key", "Idempotency-Key must be 8–128 visible ASCII characters")
			return
		}
		name, fingerprint, err := service.ImageSubmissionFingerprint(c)
		if err != nil {
			submissionError(c, 400, "invalid_request", "Cannot fingerprint image request: "+err.Error())
			return
		}
		row, fresh, err := model.ClaimImageSubmission(&model.ImageSubmission{ID: service.ImageSubmissionID(c.GetInt("id"), key), UserID: c.GetInt("id"), Fingerprint: fingerprint, Model: name, Path: c.Request.URL.Path, RequestID: c.GetString(common.RequestIdKey)})
		if errors.Is(err, model.ErrImageSubmissionConflict) {
			submissionError(c, 409, "idempotency_conflict", err.Error())
			return
		}
		if err != nil {
			submissionError(c, 503, "submission_storage_unavailable", "Cannot establish a durable submission receipt; no new execution started. Recover the original key before taking further action.")
			return
		}
		c.Header("Cache-Control", "no-store")
		c.Header("X-Image-Submission-Id", row.ID)
		c.Header("X-Original-Request-Id", row.RequestID)
		if !fresh {
			c.Header("Idempotency-Replayed", "true")
			if row.Status == "accepted" && row.ResponseComplete {
				contentType := row.ResponseType
				if contentType == "" {
					contentType = "application/json"
				}
				c.Abort()
				c.Data(row.ResponseCode, contentType, []byte(row.ResponseBody))
				return
			}
			c.Header("Retry-After", "5")
			c.AbortWithStatusJSON(202, gin.H{"data": service.ImageSubmissionDTO(row)})
			return
		}
		c.Set(service.ImageSubmissionContextKey, row)
		// Do not forward an account-scoped caller key into a shared upstream account.
		c.Request.Header.Del("Idempotency-Key")
		defer c.Request.Header.Set("Idempotency-Key", key)
		// Continue the single original attempt after a caller disconnect, within a
		// fixed deadline. No detached goroutine may retain the Gin context.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), time.Duration(model.ImageSubmissionLeaseSeconds)*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		original := c.Writer
		capture := &imageSubmissionWriter{ResponseWriter: original}
		c.Writer = capture
		finished := false
		defer func() {
			c.Writer = original
			if !finished {
				// Panic/crash during handler execution must never authorize another POST.
				_ = model.FinishImageSubmission(row, "acceptance_unknown", "", "", 0, nil, false)
			}
		}()
		c.Next()
		status := "acceptance_unknown"
		if !c.GetBool(service.ImageSubmissionExecutingKey) {
			status = "not_accepted"
		}
		var envelope map[string]any
		validJSON := common.Unmarshal(capture.body.Bytes(), &envelope) == nil && len(envelope) > 0
		responseOK := validJSON && envelope["error"] == nil && envelope["success"] != false
		if code, ok := envelope["code"].(float64); ok && code != 0 && code != 1 && (code < 200 || code >= 300) {
			responseOK = false
		}
		if status, ok := envelope["status"].(string); ok && (status == "failed" || status == "error" || status == "rejected") {
			responseOK = false
		}
		if c.GetBool(service.ImageSubmissionExecutingKey) && capture.Status() >= 200 && capture.Status() < 300 && responseOK && !capture.overflow {
			status = "accepted"
		}
		taskID := c.GetString("image_poll_task_id")
		if taskID == "" {
			taskID = original.Header().Get("X-Task-ID")
		}
		if taskID == "" && validJSON {
			if id, ok := envelope["id"].(string); ok && (strings.Contains(row.Path, "midjourney") || strings.Contains(row.Path, "/mj/")) {
				taskID = id
			}
			if data, ok := envelope["data"].([]any); ok && len(data) > 0 {
				if item, ok := data[0].(map[string]any); ok {
					if id, ok := item["task_id"].(string); ok {
						taskID = id
					}
				}
			}
		}
		body := capture.body.Bytes()
		if capture.overflow {
			body = nil
		}
		err = model.FinishImageSubmission(row, status, taskID, original.Header().Get("Content-Type"), capture.Status(), body, !capture.overflow)
		finished = err == nil
		c.Writer = original
		if err != nil {
			original.Header().Del("Content-Length")
			original.Header().Set("Content-Type", "application/json")
			c.AbortWithStatusJSON(503, gin.H{"error": gin.H{"code": "submission_storage_unavailable", "message": "Original execution may have completed; query its receipt. Do not create a replacement generation.", "retry_safe": false}})
			return
		}
		original.Header().Del("Content-Length")
		if capture.overflow {
			c.AbortWithStatusJSON(502, gin.H{"error": gin.H{"code": "submission_response_too_large", "message": "Original response exceeded recovery storage limit; do not resubmit", "retry_safe": false}})
			return
		}
		original.WriteHeader(capture.Status())
		_, _ = original.Write(body)
	}
}

// The execution boundary precedes all business middleware. A pre-boundary
// rejection cannot have reserved quota or contacted a generation provider.
func BeginImageSubmissionExecution() gin.HandlerFunc {
	return func(c *gin.Context) {
		if row := service.ImageSubmissionFromContext(c); row != nil {
			if err := model.BeginImageSubmission(row); err != nil {
				submissionError(c, 503, "submission_claim_unavailable", "Cannot begin original execution; query its receipt and do not resubmit")
				return
			}
			c.Set(service.ImageSubmissionExecutingKey, true)
		}
		c.Next()
	}
}

// Buffer until both original output and receipt are durable. Socket delivery
// failures cannot discard the only copy. Memory is bounded per request.
type imageSubmissionWriter struct {
	gin.ResponseWriter
	body        bytes.Buffer
	code        int
	overflow    bool
	wroteHeader bool
}

func (w *imageSubmissionWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.code = code
	}
}
func (w *imageSubmissionWriter) WriteHeaderNow() {
	if w.code == 0 {
		w.code = 200
	}
	w.wroteHeader = true
}
func (w *imageSubmissionWriter) Write(p []byte) (int, error) {
	w.WriteHeaderNow()
	if w.body.Len()+len(p) > model.ImageSubmissionMaxResponseBytes {
		w.overflow = true
		return len(p), nil
	}
	if w.overflow {
		return len(p), nil
	}
	return w.body.Write(p)
}
func (w *imageSubmissionWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func (w *imageSubmissionWriter) Status() int {
	if w.code == 0 {
		return 200
	}
	return w.code
}
func (w *imageSubmissionWriter) Size() int {
	if !w.wroteHeader {
		return -1
	}
	return w.body.Len()
}
func (w *imageSubmissionWriter) Written() bool { return w.wroteHeader }
func (w *imageSubmissionWriter) Flush()        { w.WriteHeaderNow() }

var _ http.Flusher = (*imageSubmissionWriter)(nil)
