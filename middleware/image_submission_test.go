package middleware

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func imageReceiptRouter(t *testing.T, before, handler gin.HandlerFunc) *gin.Engine {
	t.Helper()
	old := model.DB
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "image.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.ImageSubmission{}))
	model.DB = db
	t.Cleanup(func() { model.DB = old; sqlDB.Close() })
	r := gin.New()
	r.Use(BodyStorageCleanup(), func(c *gin.Context) { c.Set("id", 7); c.Set(common.RequestIdKey, "original-request"); c.Next() }, DurableImageSubmission())
	if before != nil {
		r.Use(before)
	}
	r.Use(BeginImageSubmissionExecution())
	r.POST("/v1/images/generations", handler)
	r.POST("/v1/images/edits", handler)
	r.POST("/v1/midjourney/generations", handler)
	return r
}
func imagePost(r *gin.Engine, key, body string) *httptest.ResponseRecorder {
	q := httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader(body))
	q.Header.Set("Content-Type", "application/json")
	q.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, q)
	return w
}
func TestImageSubmissionConcurrentNewModelsAndConflicts(t *testing.T) {
	var generations, charges atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	r := imageReceiptRouter(t, nil, func(c *gin.Context) {
		require.Empty(t, c.GetHeader("Idempotency-Key"))
		generations.Add(1)
		charges.Add(1)
		close(entered)
		<-release
		c.JSON(200, gin.H{"data": []gin.H{{"task_id": "original-task"}}})
	})
	body := `{"model":"future-model-with-no-allowlist","prompt":"boat","seed":9007199254740993}`
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- imagePost(r, "concurrent-image", body) }()
	<-entered
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := imagePost(r, "concurrent-image", body)
			require.Equal(t, 202, w.Code)
			require.Contains(t, w.Body.String(), "submitting")
		}()
	}
	wg.Wait()
	close(release)
	w := <-first
	require.Equal(t, 200, w.Code)
	row, err := model.GetImageSubmission(7, service.ImageSubmissionID(7, "concurrent-image"))
	require.NoError(t, err)
	require.Equal(t, "original-task", row.TaskID)
	require.Equal(t, w.Body.String(), string(row.ResponseBody))
	require.True(t, row.ResponseComplete)
	require.Equal(t, 200, imagePost(r, "concurrent-image", body).Code)
	for _, changed := range []string{strings.Replace(body, "boat", "car", 1), strings.Replace(body, "9007199254740993", "9007199254740992", 1), " " + body} {
		require.Equal(t, 409, imagePost(r, "concurrent-image", changed).Code)
	}
	require.EqualValues(t, 1, generations.Load())
	require.EqualValues(t, 1, charges.Load())
	_, err = model.GetImageSubmission(8, row.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}
func TestImageSubmissionUnknownVersusProvenPreExecutionRejection(t *testing.T) {
	for _, before := range []bool{false, true} {
		t.Run(map[bool]string{false: "after_execution", true: "before_execution"}[before], func(t *testing.T) {
			var executions atomic.Int32
			var gate gin.HandlerFunc
			if before {
				gate = func(c *gin.Context) { c.AbortWithStatusJSON(429, gin.H{"error": gin.H{"code": "rate_limit"}}) }
			}
			r := imageReceiptRouter(t, gate, func(c *gin.Context) {
				executions.Add(1)
				c.JSON(502, gin.H{"error": gin.H{"code": "do_request_failed"}})
			})
			body := `{"model":"any-image-model","prompt":"boat"}`
			first := imagePost(r, "unknown-image", body)
			require.True(t, first.Code == 429 || first.Code == 502)
			again := imagePost(r, "unknown-image", body)
			require.Equal(t, 202, again.Code)
			row, err := model.GetImageSubmission(7, service.ImageSubmissionID(7, "unknown-image"))
			require.NoError(t, err)
			dto := service.ImageSubmissionDTO(row)
			require.Equal(t, before, dto["retry_safe"])
			require.Equal(t, before, dto["billing_final"])
			if before {
				require.Equal(t, "not_accepted", row.Status)
				require.Equal(t, 0, dto["net_charge"])
			} else {
				require.Equal(t, "acceptance_unknown", row.Status)
				require.Nil(t, dto["net_charge"])
			}
		})
	}
}
func TestImageSubmissionMultipartStableBoundaryAndFileOrder(t *testing.T) {
	var calls int
	r := imageReceiptRouter(t, nil, func(c *gin.Context) {
		calls++
		c.JSON(200, gin.H{"data": []gin.H{{"url": "https://example.test/result.png"}}})
	})
	post := func(files []string, modelName string) *httptest.ResponseRecorder {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		require.NoError(t, writer.WriteField("model", modelName))
		require.NoError(t, writer.WriteField("prompt", "boat"))
		for _, content := range files {
			p, err := writer.CreateFormFile("image[]", common.GetUUID()+".png")
			require.NoError(t, err)
			_, err = p.Write([]byte(content))
			require.NoError(t, err)
		}
		require.NoError(t, writer.Close())
		q := httptest.NewRequest("POST", "/v1/images/edits", &body)
		q.Header.Set("Idempotency-Key", "multipart-image")
		q.Header.Set("Content-Type", writer.FormDataContentType())
		w := httptest.NewRecorder()
		r.ServeHTTP(w, q)
		return w
	}
	require.Equal(t, 200, post([]string{"first", "second"}, "future-edit-model").Code)
	require.Equal(t, 200, post([]string{"first", "second"}, "future-edit-model").Code)
	require.Equal(t, 409, post([]string{"second", "first"}, "future-edit-model").Code)
	require.Equal(t, 409, post([]string{"first", "second"}, "different-model").Code)
	require.Equal(t, 1, calls)
}
func TestImageSubmissionSurvivesCallerCancellationAndStaleClaim(t *testing.T) {
	r := imageReceiptRouter(t, nil, func(c *gin.Context) {
		require.NoError(t, c.Request.Context().Err())
		c.JSON(200, gin.H{"data": []gin.H{{"b64_json": "original-image"}}})
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	q := httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader(`{"model":"future"}`)).WithContext(ctx)
	q.Header.Set("Idempotency-Key", "cancelled-image")
	q.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, q)
	require.Equal(t, 200, w.Code)
	row, err := model.GetImageSubmission(7, service.ImageSubmissionID(7, "cancelled-image"))
	require.NoError(t, err)
	require.Contains(t, string(row.ResponseBody), "original-image")
	claim, fresh, err := model.ClaimImageSubmission(&model.ImageSubmission{ID: "crashed-owner", UserID: 7, Fingerprint: "one"})
	require.NoError(t, err)
	require.True(t, fresh)
	require.NoError(t, model.DB.Model(claim).Update("deadline", time.Now().Unix()-1).Error)
	row, err = model.GetImageSubmission(7, claim.ID)
	require.NoError(t, err)
	require.Equal(t, "pending", row.Status)
	require.Equal(t, "acceptance_unknown", service.ImageSubmissionDTO(row)["status"])
	require.False(t, service.ImageSubmissionDTO(row)["retry_safe"].(bool))
	require.Error(t, model.BeginImageSubmission(claim))
	row2, fresh, err := model.ClaimImageSubmission(&model.ImageSubmission{ID: claim.ID, UserID: 7, Fingerprint: "one"})
	require.NoError(t, err)
	require.False(t, fresh)
	require.Equal(t, row.Owner, row2.Owner)
}
func TestImageSubmissionJSONAndLegacyMJSameKeyDifferentEndpointConflict(t *testing.T) {
	r := imageReceiptRouter(t, nil, func(c *gin.Context) { c.JSON(200, gin.H{"id": "original-midjourney-batch"}) })
	body := `{"model":"midjourney-niji-7","prompt":"boat"}`
	require.Equal(t, 200, imagePost(r, "endpoint-image", body).Code)
	q := httptest.NewRequest(http.MethodPost, "/v1/midjourney/generations", strings.NewReader(body))
	q.Header.Set("Content-Type", "application/json")
	q.Header.Set("Idempotency-Key", "endpoint-image")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, q)
	require.Equal(t, 409, w.Code)
}

func TestImageSubmissionStorageFailureNeverRunsOrReexecutes(t *testing.T) {
	var calls int
	r := imageReceiptRouter(t, nil, func(c *gin.Context) {
		calls++
		c.JSON(200, gin.H{"data": []gin.H{{"url": "https://example.test/original.png"}}})
	})
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register("test:fail_finalize", func(tx *gorm.DB) {
		if updates, ok := tx.Statement.Dest.(map[string]any); ok {
			if _, exists := updates["response_body"]; exists {
				tx.AddError(context.DeadlineExceeded)
			}
		}
	}))
	body := `{"model":"future"}`
	w := imagePost(r, "failed-persist-image", body)
	require.Equal(t, 503, w.Code)
	require.Contains(t, w.Body.String(), "submission_storage_unavailable")
	require.Contains(t, w.Body.String(), `"retry_safe":false`)
	w = imagePost(r, "failed-persist-image", body)
	require.Equal(t, 202, w.Code)
	require.Equal(t, 1, calls)
	require.NoError(t, model.DB.Callback().Create().Before("gorm:create").Register("test:fail_claim", func(tx *gorm.DB) { tx.AddError(context.DeadlineExceeded) }))
	w = imagePost(r, "claim-storage-fails", body)
	require.Equal(t, 503, w.Code)
	require.Equal(t, 1, calls)
}

func TestImageSubmissionPanicPreservesUnknownAndUnkeyedBehavior(t *testing.T) {
	var calls int
	r := imageReceiptRouter(t, nil, func(c *gin.Context) { calls++; panic("interrupted execution") })
	// Recovery must wrap the durable middleware so its defer freezes unknown first.
	outer := gin.New()
	outer.Use(gin.Recovery())
	outer.Any("/*path", func(c *gin.Context) { r.ServeHTTP(c.Writer, c.Request) })
	body := `{"model":"future"}`
	require.Equal(t, 500, imagePost(outer, "panic-image-key", body).Code)
	require.Equal(t, 202, imagePost(outer, "panic-image-key", body).Code)
	require.Equal(t, 1, calls)
	row, err := model.GetImageSubmission(7, service.ImageSubmissionID(7, "panic-image-key"))
	require.NoError(t, err)
	require.Equal(t, "acceptance_unknown", row.Status)
	require.Equal(t, 500, imagePost(outer, "", body).Code)
	require.Equal(t, 500, imagePost(outer, "", body).Code)
	require.Equal(t, 3, calls)
}

type failedImageSocket struct{ *httptest.ResponseRecorder }

func (failedImageSocket) Write(p []byte) (int, error) { return 0, context.Canceled }
func TestImageSubmissionResultDurableBeforeSocketWrite(t *testing.T) {
	r := imageReceiptRouter(t, nil, func(c *gin.Context) { c.JSON(200, gin.H{"data": []gin.H{{"b64_json": "saved-before-delivery"}}}) })
	q := httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader(`{"model":"future"}`))
	q.Header.Set("Content-Type", "application/json")
	q.Header.Set("Idempotency-Key", "socket-lost-image")
	r.ServeHTTP(failedImageSocket{httptest.NewRecorder()}, q)
	row, err := model.GetImageSubmission(7, service.ImageSubmissionID(7, "socket-lost-image"))
	require.NoError(t, err)
	require.Equal(t, "accepted", row.Status)
	require.Contains(t, string(row.ResponseBody), "saved-before-delivery")
	w := imagePost(r, "socket-lost-image", `{"model":"future"}`)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "saved-before-delivery")
	require.Equal(t, "true", w.Header().Get("Idempotency-Replayed"))
}

func TestImageSubmissionPreservesGinHeaderCommitSemantics(t *testing.T) {
	r := imageReceiptRouter(t, nil, func(c *gin.Context) {
		c.Status(200)
		require.False(t, c.Writer.Written())
		c.JSON(400, gin.H{"error": gin.H{"code": "invalid_request"}})
	})
	w := imagePost(r, "header-semantics-key", `{"model":"future"}`)
	require.Equal(t, 400, w.Code)
	row, err := model.GetImageSubmission(7, service.ImageSubmissionID(7, "header-semantics-key"))
	require.NoError(t, err)
	require.Equal(t, 400, row.ResponseCode)
}
