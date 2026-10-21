package service

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

const ImageSubmissionContextKey = "durable_image_submission"
const ImageSubmissionExecutingKey = "durable_image_submission_executing"

// Endpoint based, never a model allowlist. New models on these APIs inherit it.
func IsImageSubmissionPath(path string) bool {
	switch path {
	case "/v1/chat/completions", "/v1/responses", "/v1/edits", "/v1/images/generations", "/v1/images/generations/async", "/v1/images/edits", "/v1/images/edits/async", "/v1/images/variations", "/v1/midjourney/generations", "/v1/midjourney/generations/imagine":
		return true
	}
	if (strings.HasPrefix(path, "/v1/models/") || strings.HasPrefix(path, "/v1beta/models/")) && (strings.HasSuffix(path, ":generateContent") || strings.HasSuffix(path, ":streamGenerateContent") || strings.HasSuffix(path, ":predict")) {
		return true
	}
	return strings.Contains(path, "/mj/submit/") || strings.HasSuffix(path, "/mj/insight-face/swap")
}
func IsImageSubmissionRequest(c *gin.Context) bool {
	return c.Request.Method == http.MethodPost && IsImageSubmissionPath(c.Request.URL.Path)
}
func ImageSubmissionID(userID int, key string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte("image-submission-v1:"+strconv.Itoa(userID)+":"+key)))
}
func ImageSubmissionFromContext(c *gin.Context) *model.ImageSubmission {
	if c == nil {
		return nil
	}
	value, _ := c.Get(ImageSubmissionContextKey)
	row, _ := value.(*model.ImageSubmission)
	return row
}
func HasImageSubmission(c *gin.Context) bool { return ImageSubmissionFromContext(c) != nil }

func ImageSubmissionDTO(row *model.ImageSubmission) map[string]any {
	status := row.Status
	if (status == "pending" || status == "submitting") && row.Deadline <= time.Now().Unix() {
		status = "acceptance_unknown"
	}
	safe := status == "not_accepted"
	billing := "unverified"
	var net any
	if safe {
		billing = "final_zero"
		net = 0
	}
	action := "query_submission"
	if status == "acceptance_unknown" {
		action = "contact_support_do_not_resubmit"
	}
	if safe {
		action = "correct_error_then_use_new_key"
	}
	if status == "accepted" && row.ResponseComplete {
		action = "retrieve_original_response"
	}
	return map[string]any{
		"submission_id": row.ID, "request_id": row.RequestID, "model": row.Model, "endpoint": row.Path,
		"status": status, "acceptance_final": status == "accepted" || safe,
		"task_id": row.TaskID, "response_available": row.ResponseComplete,
		"original_http_status": row.ResponseCode,
		"same_key_replay_safe": true, "new_submission_safe": safe, "retry_safe": safe,
		"billing_status": billing, "billing_final": safe, "net_charge": net,
		"next_action": action, "created_at": row.CreatedAt, "updated_at": row.UpdatedAt,
		"retention":  "at_least_30_days_no_automatic_key_reuse",
		"lookup_url": "/v1/image-submissions/by-key", "response_url": "/v1/image-submissions/by-key/response",
	}
}
