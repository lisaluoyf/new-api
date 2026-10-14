package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func GetVideoSubmissionByKey(c *gin.Context) {
	key := c.GetHeader("Idempotency-Key")
	if !service.ValidVideoSubmissionKey(key) {
		c.JSON(400, gin.H{"error": gin.H{"code": "invalid_idempotency_key", "message": "Idempotency-Key must be 8–128 visible ASCII characters"}})
		return
	}
	record, err := model.GetVideoSubmission(c.GetInt("id"), service.VideoSubmissionID(c.GetInt("id"), key))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		legacyID := "video_request_" + service.SeedanceKeyFingerprint(strconv.Itoa(c.GetInt("id")) + ":" + key)[:32]
		if legacy, e := model.GetSeedanceResource(c.GetInt("id"), "video_request", legacyID); e == nil {
			c.JSON(200, gin.H{"code": 200, "data": service.SeedanceVideoRequestDTO(legacy)})
			return
		} else if !errors.Is(e, gorm.ErrRecordNotFound) {
			err = e
		} else {
			// A missing receipt does not exclude a racing/in-flight original request,
			// a pre-feature submission, or a submission made without a key.
			c.JSON(404, gin.H{"error": gin.H{"code": "submission_not_found", "message": "No receipt is currently visible for this account and key. This is not proof that an in-flight request was not accepted.", "retry_safe": false}})
			return
		}
	}
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"code": "submission_storage_unavailable", "message": "Submission lookup is temporarily unavailable"}})
		return
	}
	c.JSON(200, gin.H{"code": 200, "data": service.VideoSubmissionDTO(record)})
}
