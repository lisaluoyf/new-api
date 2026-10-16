package middleware

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func durableSubmissionRouter(t *testing.T, handler gin.HandlerFunc) *gin.Engine {
	t.Helper()
	old := model.DB
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "durable.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.VideoSubmission{}, &model.SeedanceResource{}))
	model.DB = db
	t.Cleanup(func() { model.DB = old; sqlDB.Close() })
	r := gin.New()
	r.Use(BodyStorageCleanup(), func(c *gin.Context) { c.Set("id", 7); c.Set(common.RequestIdKey, "trace-original"); c.Next() })
	for _, path := range []string{"/v1/videos", "/v1/video/generations", "/v1/videos/generations"} {
		r.POST(path, DurableSeedanceSubmission(), handler)
	}
	return r
}
func postSubmission(r *gin.Engine, path, key, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	q := httptest.NewRequest("POST", path, strings.NewReader(body))
	q.Header.Set("Content-Type", "application/json")
	q.Header.Set("Idempotency-Key", key)
	r.ServeHTTP(w, q)
	return w
}
func TestDurableSubmissionConcurrentReplayDoesNotSubmitOrChargeAgain(t *testing.T) {
	var submits, charges atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	r := durableSubmissionRouter(t, func(c *gin.Context) {
		require.NoError(t, service.MarkVideoSubmissionSending(c, 9))
		submits.Add(1)
		charges.Add(1)
		close(entered)
		<-release
		record := service.VideoSubmissionFromContext(c)
		require.NoError(t, model.FinishVideoSubmission(record.ID, model.VideoSubmissionAccepted, "upstream-secret", true))
		c.JSON(200, gin.H{"task_id": record.TaskID})
	})
	body := `{"model":"seedance-2.0","prompt":"boat","duration":4}`
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- postSubmission(r, "/v1/videos", "concurrent-001", body) }()
	<-entered
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := postSubmission(r, []string{"/v1/video/generations", "/v1/videos/generations"}[i%2], "concurrent-001", `{"duration":4, "prompt":"boat", "model":"seedance-2.0"}`)
			require.Equal(t, 202, w.Code)
			require.Contains(t, w.Body.String(), "submitting")
			require.NotContains(t, w.Body.String(), "upstream-secret")
		}(i)
	}
	wg.Wait()
	close(release)
	first := <-done
	require.Equal(t, 200, first.Code)
	again := postSubmission(r, "/v1/videos", "concurrent-001", body)
	require.Equal(t, 202, again.Code)
	require.Contains(t, again.Body.String(), "accepted")
	require.Equal(t, "true", again.Header().Get("Idempotency-Replayed"))
	require.EqualValues(t, 1, submits.Load())
	require.EqualValues(t, 1, charges.Load())
	changed := postSubmission(r, "/v1/videos", "concurrent-001", strings.Replace(body, "boat", "car", 1))
	require.Equal(t, 409, changed.Code)
}
func TestDurableSubmissionAmbiguousAndPreSendFailures(t *testing.T) {
	for _, send := range []bool{false, true} {
		t.Run(fmt.Sprint(send), func(t *testing.T) {
			var calls atomic.Int32
			r := durableSubmissionRouter(t, func(c *gin.Context) {
				calls.Add(1)
				if send {
					require.NoError(t, service.MarkVideoSubmissionSending(c, 9))
				}
				c.JSON(502, gin.H{"error": "lost response"})
			})
			body := `{"model":"seedance-2.5","prompt":"boat"}`
			require.Equal(t, 502, postSubmission(r, "/v1/videos", "failure-001", body).Code)
			again := postSubmission(r, "/v1/videos", "failure-001", body)
			require.Equal(t, 202, again.Code)
			state := "not_accepted"
			if send {
				state = "acceptance_unknown"
			}
			require.Contains(t, again.Body.String(), state)
			require.EqualValues(t, 1, calls.Load())
		})
	}
}
func TestDurableSubmissionFingerprintPrecisionNotificationsAndMultipart(t *testing.T) {
	r := durableSubmissionRouter(t, func(c *gin.Context) { c.Status(400) })
	body := `{"model":"seedance-2.0","seed":9007199254740992,"notifications":{"webhook":{"endpoint_id":"one"}}}`
	require.Equal(t, 400, postSubmission(r, "/v1/videos", "precision-001", body).Code)
	require.Equal(t, 409, postSubmission(r, "/v1/videos", "precision-001", strings.Replace(body, "9007199254740992", "9007199254740993", 1)).Code)
	require.Equal(t, 409, postSubmission(r, "/v1/videos", "precision-001", strings.Replace(body, "one", "two", 1)).Code)
	multipartPost := func(content, filename string) *httptest.ResponseRecorder {
		var b bytes.Buffer
		writer := multipart.NewWriter(&b)
		require.NoError(t, writer.WriteField("model", "seedance-2.0"))
		require.NoError(t, writer.WriteField("prompt", "boat"))
		f, err := writer.CreateFormFile("input_reference", filename)
		require.NoError(t, err)
		_, err = f.Write([]byte(content))
		require.NoError(t, err)
		require.NoError(t, writer.Close())
		q := httptest.NewRequest("POST", "/v1/videos", &b)
		q.Header.Set("Content-Type", writer.FormDataContentType())
		q.Header.Set("Idempotency-Key", "multipart-001")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, q)
		return w
	}
	require.Equal(t, 400, multipartPost("same-bytes", "a.png").Code)
	require.Equal(t, 202, multipartPost("same-bytes", "b.png").Code)
	require.Equal(t, 409, multipartPost("changed-bytes", "a.png").Code)
	require.Equal(t, 400, postSubmission(r, "/v1/videos", "short", body).Code)
	require.Equal(t, 400, postSubmission(r, "/v1/videos", "unsupported-001", `{"model":"sora-2"}`).Code)
}

func TestDurableSubmissionAutomaticReviewRejectionIsNotAmbiguous(t *testing.T) {
	r := durableSubmissionRouter(t, func(c *gin.Context) {
		require.NoError(t, service.MarkVideoSubmissionSending(c, 292))
		c.Set("xingchi_automatic_review_rejected", true)
		c.JSON(400, gin.H{"error": "Image did not pass material review"})
	})
	body := `{"model":"seedance-2.5","prompt":"cartoon"}`
	require.Equal(t, 400, postSubmission(r, "/v1/videos", "review-rejected-001", body).Code)
	replay := postSubmission(r, "/v1/videos", "review-rejected-001", body)
	require.Equal(t, 202, replay.Code)
	require.Contains(t, replay.Body.String(), "not_accepted")
	require.NotContains(t, replay.Body.String(), "acceptance_unknown")
}
