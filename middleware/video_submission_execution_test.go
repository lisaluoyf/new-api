package middleware

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type disconnectedReviewTransport func(*http.Request) (*http.Response, error)

func (f disconnectedReviewTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestDurableSubmissionCompletesMaterialReviewAfterDisconnect(t *testing.T) {
	for _, stage := range []string{"preparation", "rejection", "CreateAssetGroup", "CreateAsset", "GetAsset", "reviewed_video", "persistence"} {
		t.Run(stage, func(t *testing.T) {
			parent, disconnect := context.WithCancel(context.Background())
			defer disconnect()
			cancelAt := func(at string) {
				if at == stage {
					disconnect()
				}
			}
			var execution context.Context
			sends, actions := 0, 0
			const body = `{"model":"seedance-2.5","content":[{"type":"text","text":"synthetic character"},{"type":"image_url","image_url":{"url":"https://example.com/synthetic.png"},"role":"reference_image"}]}`
			r := durableSubmissionRouter(t, func(c *gin.Context) {
				execution = c.Request.Context()
				cancelAt("preparation")
				require.NoError(t, execution.Err())
				require.NoError(t, service.MarkVideoSubmissionSending(c, 292))
				record := service.VideoSubmissionFromContext(c)
				resp, err := service.DoXingchiAutomaticReview(c, 292, "seedance-2.5", "fixture-key", record.TaskID, strings.NewReader(body), func(raw io.Reader) (*http.Response, error) {
					sends++
					status, response := 400, `{"error":{"code":"InputImageSensitiveContentDetected.PrivacyInformation"}}`
					if sends == 1 {
						cancelAt("rejection")
					} else {
						cancelAt("reviewed_video")
						data, e := io.ReadAll(raw)
						require.NoError(t, e)
						require.Contains(t, string(data), "asset://upstream-asset")
						status, response = 200, `{"id":"accepted-original"}`
					}
					require.NoError(t, c.Request.Context().Err())
					return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response))}, nil
				})
				require.NoError(t, err)
				require.Equal(t, 200, resp.StatusCode)
				require.NoError(t, resp.Body.Close())
				cancelAt("persistence")
				require.NoError(t, c.Request.Context().Err())
				task := &model.Task{TaskID: record.TaskID, UserId: 7, ChannelId: 292, Status: model.TaskStatusSubmitted, Properties: model.Properties{OriginModelName: "seedance-2.5"}, PrivateData: model.TaskPrivateData{UpstreamTaskID: "accepted-original"}}
				require.NoError(t, model.InsertVideoSubmissionTask(task, nil, record.ID))
				c.JSON(200, gin.H{"task_id": task.TaskID})
			})
			require.NoError(t, model.DB.AutoMigrate(&model.Channel{}))
			oldCache := common.MemoryCacheEnabled
			common.MemoryCacheEnabled = false
			t.Cleanup(func() { common.MemoryCacheEnabled = oldCache })
			base := "https://api.xingchiyun.com/v1/seedance"
			require.NoError(t, model.DB.Create(&model.Channel{Id: 292, Type: 54, Key: "fixture-key", BaseURL: &base, Status: 1}).Error)
			oldTransport := http.DefaultTransport
			http.DefaultTransport = disconnectedReviewTransport(func(req *http.Request) (*http.Response, error) {
				action := req.URL.Query().Get("Action")
				cancelAt(action)
				require.NoError(t, req.Context().Err())
				actions++
				response := `{"Result":{"Id":"upstream-group"}}`
				switch action {
				case "CreateAsset":
					response = `{"Result":{"Id":"upstream-asset"}}`
				case "GetAsset":
					response = `{"Result":{"Id":"upstream-asset","Status":"Active"}}`
				case "CreateAssetGroup":
				default:
					t.Fatalf("unexpected action %s", action)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response))}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = oldTransport })
			req := httptest.NewRequest("POST", "/v1/videos/generations", strings.NewReader(body)).WithContext(parent)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "disconnect-review-001")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, 200, w.Code)
			require.ErrorIs(t, parent.Err(), context.Canceled)
			require.ErrorIs(t, execution.Err(), context.Canceled) // released after persistence
			require.Equal(t, 2, sends)                            // one confirmed rejection, one reviewed submit
			require.Equal(t, 3, actions)
			replay := postSubmission(r, "/v1/videos/generations", "disconnect-review-001", body)
			require.Equal(t, 202, replay.Code)
			require.Contains(t, replay.Body.String(), `"status":"accepted"`)
			require.Equal(t, 2, sends)
			var count int64
			require.NoError(t, model.DB.Model(&model.Task{}).Count(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}

func TestDurableSubmissionExecutionCoversAllModelsRoutesAndFutureChannels(t *testing.T) {
	for i, name := range []string{"seedance-2.0", "seedance-2.0-fast", "seedance-2.0-mini", "seedance-2.5"} {
		for _, path := range []string{"/v1/videos", "/v1/video/generations", "/v1/videos/generations"} {
			t.Run(name+path, func(t *testing.T) {
				parent, disconnect := context.WithCancel(context.WithValue(context.Background(), "fixture", "retained"))
				defer disconnect()
				r := durableSubmissionRouter(t, func(c *gin.Context) {
					disconnect()
					require.NoError(t, c.Request.Context().Err())
					require.Equal(t, "retained", c.Request.Context().Value("fixture"))
					deadline, ok := c.Request.Context().Deadline()
					require.True(t, ok)
					require.Positive(t, time.Until(deadline))
					require.LessOrEqual(t, time.Until(deadline), 5*time.Minute)
					require.NoError(t, service.MarkVideoSubmissionSending(c, 9000+i))
					c.Status(502)
				})
				req := httptest.NewRequest("POST", path, strings.NewReader(fmt.Sprintf(`{"model":%q}`, name))).WithContext(parent)
				req.Header.Set("Content-Type", "application/json") // also covers generated keys
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				require.Equal(t, 502, w.Code)
				var record model.VideoSubmission
				require.NoError(t, model.DB.First(&record).Error)
				require.Equal(t, model.VideoSubmissionUnknown, record.Status)
			})
		}
	}
}

func TestDurableSubmissionDoesNotClaimAnAlreadyDisconnectedRequest(t *testing.T) {
	r := durableSubmissionRouter(t, func(c *gin.Context) { t.Fatal("must not execute") })
	parent, disconnect := context.WithCancel(context.Background())
	disconnect()
	req := httptest.NewRequest("POST", "/v1/videos", strings.NewReader(`{"model":"seedance-2.5"}`)).WithContext(parent)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 499, w.Code)
	var count int64
	require.NoError(t, model.DB.Model(&model.VideoSubmission{}).Count(&count).Error)
	require.Zero(t, count)
}
