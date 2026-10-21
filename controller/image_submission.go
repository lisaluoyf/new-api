package controller

import (
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// No upstream calls, polling, billing, lease transitions or writes on lookup.
func GetImageSubmissionByKey(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	key := c.GetHeader("Idempotency-Key")
	if !service.ValidVideoSubmissionKey(key) {
		c.JSON(400, gin.H{"error": gin.H{"code": "invalid_idempotency_key", "message": "Idempotency-Key must be 8–128 visible ASCII characters"}})
		return
	}
	row, err := model.GetImageSubmission(c.GetInt("id"), service.ImageSubmissionID(c.GetInt("id"), key))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(404, gin.H{"error": gin.H{"code": "submission_not_found", "message": "No receipt is currently visible. This does not prove that an original request was not accepted.", "retry_safe": false}})
		return
	}
	if err != nil {
		c.JSON(503, gin.H{"error": gin.H{"code": "submission_storage_unavailable", "message": "Submission lookup is temporarily unavailable", "retry_safe": false}})
		return
	}
	c.Header("X-Image-Submission-Id", row.ID)
	c.Header("X-Original-Request-Id", row.RequestID)
	if strings.HasSuffix(c.Request.URL.Path, "/response") {
		if !row.ResponseComplete {
			c.JSON(202, gin.H{"data": service.ImageSubmissionDTO(row)})
			return
		}
		contentType := row.ResponseType
		if contentType == "" {
			contentType = "application/json"
		}
		c.Data(row.ResponseCode, contentType, []byte(row.ResponseBody))
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": service.ImageSubmissionDTO(row)})
}
