package middleware

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Runs after authentication, before notifications, asset I/O, distribution or
// billing. Replays never execute downstream middleware or the relay handler.
func DurableSeedanceSubmission() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.GetHeader("Idempotency-Key")
		if c.Request.Method != http.MethodPost || !service.IsSeedanceSubmissionPath(c.Request.URL.Path) || key == "" {
			c.Next()
			return
		}
		if !service.ValidVideoSubmissionKey(key) {
			submissionError(c, 400, "invalid_idempotency_key", "Idempotency-Key must be 8–128 visible ASCII characters")
			return
		}
		name, fingerprint, err := service.VideoSubmissionFingerprint(c)
		if err != nil {
			submissionError(c, 400, "invalid_request", err.Error())
			return
		}
		if !service.IsSeedanceLibraryModel(name) {
			submissionError(c, 400, "idempotency_not_supported", "Idempotency-Key is supported only for Seedance video generation on this endpoint")
			return
		}
		// Older portrait receipts already protect keys accepted before this feature.
		// Keep their original validation/replay path, rather than creating a second claim.
		legacyID := "video_request_" + service.SeedanceKeyFingerprint(strconv.Itoa(c.GetInt("id")) + ":" + key)[:32]
		if _, err := model.GetSeedanceResource(c.GetInt("id"), "video_request", legacyID); err == nil {
			c.Set("video_submission_legacy", true)
			c.Next()
			return
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			submissionError(c, 503, "submission_storage_unavailable", "Submission storage is unavailable; no new submission was started")
			return
		}
		candidate := &model.VideoSubmission{ID: service.VideoSubmissionID(c.GetInt("id"), key), UserID: c.GetInt("id"), Fingerprint: fingerprint, Model: name, TaskID: model.GenerateTaskID(), RequestID: c.GetString(common.RequestIdKey)}
		record, fresh, err := model.ClaimVideoSubmission(candidate)
		if errors.Is(err, model.ErrVideoSubmissionConflict) {
			submissionError(c, 409, "idempotency_conflict", err.Error())
			return
		}
		if err != nil {
			submissionError(c, 503, "submission_storage_unavailable", "Submission storage is unavailable; no new submission was started")
			return
		}
		c.Header("X-Video-Submission-Id", record.ID)
		if !fresh {
			c.Header("Idempotency-Replayed", "true")
			c.Header("Retry-After", "5")
			c.AbortWithStatusJSON(202, gin.H{"code": 202, "data": service.VideoSubmissionDTO(record)})
			return
		}
		c.Set(service.VideoSubmissionContextKey, record)
		// Even a panic or a downstream middleware rejection leaves a recoverable
		// receipt. After send began, failures are conservative and never requeued.
		defer func() {
			status := model.VideoSubmissionNotAccepted
			if c.GetBool(service.VideoSubmissionSentContextKey) && !c.GetBool("xingchi_automatic_review_rejected") && !c.GetBool("seedance_confirmed_not_accepted") {
				status = model.VideoSubmissionUnknown
			}
			current, err := model.GetVideoSubmission(record.UserID, record.ID)
			if err == nil && (current.Status == model.VideoSubmissionPending || current.Status == model.VideoSubmissionSubmitting) {
				if err := model.FinishVideoSubmission(record.ID, status, "", false); err != nil {
					common.SysError("video submission finalization failed: " + err.Error())
				}
			}
		}()
		c.Next()
	}
}
func submissionError(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}
