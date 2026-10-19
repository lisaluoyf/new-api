package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestVideoSubmissionLookupOwnerMissingAndLegacy(t *testing.T) {
	db := videoFailureDB(t)
	require.NoError(t, db.AutoMigrate(&model.VideoSubmission{}, &model.VideoSubmissionRefund{}, &model.BillingHold{}, &model.SeedanceResource{}))
	key := "lookup-key-001"
	record, _, err := model.ClaimVideoSubmission(&model.VideoSubmission{ID: service.VideoSubmissionID(42, key), UserID: 42, Fingerprint: "private-hash", TaskID: "task_public", Model: "seedance-2.0", RequestID: "trace_original"})
	require.NoError(t, err)
	require.NoError(t, model.MarkVideoSubmissionSending(record.ID, 1))
	require.NoError(t, model.FinishVideoSubmission(record.ID, model.VideoSubmissionAccepted, "private-upstream", true))
	r := gin.New()
	r.GET("/v1/video-submissions/by-key", func(c *gin.Context) {
		if c.GetHeader("Test-Owner") == "foreign" {
			c.Set("id", 43)
		} else {
			c.Set("id", 42)
		}
		GetVideoSubmissionByKey(c)
	})
	for _, owner := range []string{"own", "foreign"} {
		q := httptest.NewRequest("GET", "/v1/video-submissions/by-key", nil)
		q.Header.Set("Idempotency-Key", key)
		q.Header.Set("Test-Owner", owner)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, q)
		if owner == "own" {
			require.Equal(t, 200, w.Code)
			require.Contains(t, w.Body.String(), "task_public")
			require.Contains(t, w.Body.String(), "trace_original")
		} else {
			require.Equal(t, 404, w.Code)
			require.Contains(t, w.Body.String(), `"retry_safe":false`)
		}
		for _, secret := range []string{"private-upstream", "private-hash", record.OwnerToken, key} {
			require.NotContains(t, w.Body.String(), secret)
		}
	}
	legacyID := "video_request_" + service.SeedanceKeyFingerprint("42:legacy-key-001")[:32]
	require.NoError(t, db.Create(&model.SeedanceResource{ID: legacyID, Kind: "video_request", UserID: 42, UpstreamID: "task_legacy", Status: "submission_unknown"}).Error)
	q := httptest.NewRequest("GET", "/v1/video-submissions/by-key", nil)
	q.Header.Set("Idempotency-Key", "legacy-key-001")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, q)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "task_legacy")
	payload, err := common.Marshal(service.VideoSubmissionDTO(record))
	require.NoError(t, err)
	require.NotContains(t, string(payload), "private-upstream")
}
