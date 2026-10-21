package controller

import (
	"errors"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestImageSubmissionLookupReadOnlyOriginalResultAndIsolation(t *testing.T) {
	old := model.DB
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "receipt.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	model.DB = db
	t.Cleanup(func() { model.DB = old; sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&model.ImageSubmission{}))
	key := "private-original-key"
	row, fresh, err := model.ClaimImageSubmission(&model.ImageSubmission{ID: service.ImageSubmissionID(7, key), UserID: 7, Fingerprint: "private-fingerprint", RequestID: "original-request", Model: "future-image-model"})
	require.NoError(t, err)
	require.True(t, fresh)
	require.NoError(t, model.BeginImageSubmission(row))
	body := []byte(`{"data":[{"b64_json":"original-image"}]}`)
	require.NoError(t, model.FinishImageSubmission(row, "accepted", "original-task", "application/json", 200, body, true))
	stale, _, err := model.ClaimImageSubmission(&model.ImageSubmission{ID: service.ImageSubmissionID(7, "stale-original-key"), UserID: 7, Fingerprint: "same"})
	require.NoError(t, err)
	require.NoError(t, db.Model(stale).Update("deadline", time.Now().Unix()-1).Error)
	writes := 0
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:forbid_lookup_write", func(*gorm.DB) { writes++ }))
	r := gin.New()
	r.GET("/v1/image-submissions/by-key", func(c *gin.Context) {
		if c.GetHeader("Test-User") == "foreign" {
			c.Set("id", 8)
		} else {
			c.Set("id", 7)
		}
		GetImageSubmissionByKey(c)
	})
	r.GET("/v1/image-submissions/by-key/response", func(c *gin.Context) { c.Set("id", 7); GetImageSubmissionByKey(c) })
	query := func(path, key, user string) *httptest.ResponseRecorder {
		q := httptest.NewRequest("GET", path, nil)
		q.Header.Set("Idempotency-Key", key)
		q.Header.Set("Test-User", user)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, q)
		return w
	}
	w := query("/v1/image-submissions/by-key", key, "")
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "original-task")
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	for _, secret := range []string{key, "private-fingerprint", row.Owner, "original-image"} {
		require.NotContains(t, w.Body.String(), secret)
	}
	w = query("/v1/image-submissions/by-key/response", key, "")
	require.Equal(t, 200, w.Code)
	require.Equal(t, string(body), w.Body.String())
	require.Equal(t, "original-request", w.Header().Get("X-Original-Request-Id"))
	w = query("/v1/image-submissions/by-key", key, "foreign")
	require.Equal(t, 404, w.Code)
	require.Contains(t, w.Body.String(), `"retry_safe":false`)
	w = query("/v1/image-submissions/by-key", "stale-original-key", "")
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "acceptance_unknown")
	require.Contains(t, w.Body.String(), `"retry_safe":false`)
	w = query("/v1/image-submissions/by-key/response", "stale-original-key", "")
	require.Equal(t, 202, w.Code)
	require.Equal(t, 0, writes)
}

func TestImageSubmissionDisablesChannelRetryAndSpeculativeHedge(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(service.ImageSubmissionContextKey, &model.ImageSubmission{ID: "original"})
	failure := types.NewErrorWithStatusCode(errors.New("temporary transport failure"), types.ErrorCodeDoRequestFailed, 502)
	require.False(t, evaluateRetry(c, failure, 0, 5).ShouldRetry)
	require.False(t, shouldRetryForOfficialFallback(c, 0))
	require.False(t, shouldRetryForOfficialFallbackModel(c, 0))
	_, allowed := shouldClientGoneHedge(c, &relaycommon.RelayInfo{}, types.RelayFormatOpenAIImage, 0)
	require.False(t, allowed)
}
