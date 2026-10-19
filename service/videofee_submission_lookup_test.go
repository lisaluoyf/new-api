package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type videoFeeLookupTransport func(*http.Request) (*http.Response, error)

func (f videoFeeLookupTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestVideoFeeReadOnlyLookupProtocolAndErrors(t *testing.T) {
	key := videoFeeSubmissionKeyPrefix + "task_original"
	for _, tc := range []struct {
		name   string
		status int
		body   string
		good   bool
	}{
		{"unknown", 200, "", true},
		{"missing_is_not_rejected", 404, `{}`, false},
		{"unauthorized", 401, `{}`, false},
		{"unavailable", 503, `{}`, false},
		{"redirect", 307, `{}`, false},
		{"invalid_json", 200, `{`, false},
		{"oversized", 200, strings.Repeat("x", 128*1024+1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: videoFeeLookupTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "https://seedance2026.vip/v1/submissions/by-key", r.URL.String())
				require.Nil(t, r.Body)
				require.Equal(t, "Bearer original-secret", r.Header.Get("Authorization"))
				require.Equal(t, key, r.Header.Get("Idempotency-Key"))
				body := tc.body
				if tc.good {
					raw, e := common.Marshal(model.VideoFeeSubmissionObservation{Object: "submission", Key: key, Acceptance: "acceptance_unknown", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)})
					require.NoError(t, e)
					body = string(raw)
				}
				h := make(http.Header)
				h.Set("Location", "https://other.invalid/v1/video/generations")
				return &http.Response{StatusCode: tc.status, Header: h, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			observation, err := fetchVideoFeeSubmission(context.Background(), client, key, "original-secret")
			if tc.good {
				require.NoError(t, err)
				require.Equal(t, "acceptance_unknown", observation.Acceptance)
				require.False(t, observation.NewSubmissionSafe)
			} else {
				require.Error(t, err)
				require.Nil(t, observation)
				require.NotContains(t, err.Error(), "original-secret")
			}
			require.Equal(t, 1, calls)
		})
	}
}

func TestVideoFeePublicEvidenceDoesNotExposeSupplierSecretsOrPrices(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	url := "https://seedance2026.vip/private.mp4?secret=supplier"
	o := model.VideoFeeSubmissionObservation{Object: "submission", Key: "supplier-idempotency-key", Acceptance: "accepted", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), TaskID: &id, Task: &model.VideoFeeTaskObservation{ID: id, Status: "submission_unknown", ResultURL: &url}, Billing: model.VideoFeeBillingFacts{Currency: "CNY"}}
	raw, err := common.Marshal(o)
	require.NoError(t, err)
	row := model.VideoSubmission{Status: model.VideoSubmissionUnknown}
	row.ProviderObservation = model.VideoFeeEvidenceJSON(raw)
	data := VideoSubmissionDTO(&row)
	encoded, err := common.Marshal(data)
	require.NoError(t, err)
	for _, secret := range []string{id, url, "supplier-idempotency-key", "CNY"} {
		require.NotContains(t, string(encoded), secret)
	}
	require.Equal(t, false, data["new_submission_safe"])
	details := data["reconciliation"].(map[string]any)
	require.Equal(t, "accepted", details["acceptance_status"])
	require.Equal(t, "submission_unknown", details["task_status"])
}

func TestVideoFeeWorkerFindsOriginalWithoutCreationOrMoneyMutation(t *testing.T) {
	oldDB, oldClient := model.DB, httpClient
	t.Cleanup(func() { model.DB = oldDB; httpClient = oldClient })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { conn.Close() })
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.VideoSubmission{}, &model.Task{}, &model.Channel{}, &model.User{}))
	base := "https://seedance2026.vip"
	require.NoError(t, db.Create(&model.Channel{Id: 273, Key: "original-key", BaseURL: &base, Status: 1}).Error)
	require.NoError(t, db.Create(&model.User{Id: 7, Username: "original", Quota: 7000}).Error)
	row := model.VideoSubmission{ID: "lookup-worker", UserID: 7, TaskID: "task_worker_original", ChannelID: 273, Model: "seedance-2.5", Status: model.VideoSubmissionUnknown, CreatedAt: time.Now().Add(-time.Minute).Unix(), UpdatedAt: time.Now().Add(-time.Minute).Unix()}
	row.ProviderLookupEnabled = true
	snapshot := model.VideoSubmissionRecovery{Task: model.Task{TaskID: row.TaskID, UserId: 7, ChannelId: 273, Quota: 0, Properties: model.Properties{OriginModelName: row.Model}}, CredentialFingerprint: model.VideoFeeCredentialFingerprint("original-key")}
	raw, err := common.Marshal(snapshot)
	require.NoError(t, err)
	require.NoError(t, db.Create(&row).Error)
	require.NoError(t, db.Model(&row).UpdateColumns(map[string]any{"recovery_json": string(raw), "updated_at": time.Now().Add(-time.Minute).Unix()}).Error)
	id := "11111111-1111-4111-8111-111111111111"
	calls := 0
	providerStatus := "submission_unknown"
	polls := 0
	httpClient = &http.Client{Transport: videoFeeLookupTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, http.MethodGet, r.Method)
		if r.URL.Path == "/v1/videos/"+id {
			polls++
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"succeeded"}`)), Header: make(http.Header)}, nil
		}
		require.Equal(t, "/v1/submissions/by-key", r.URL.Path)
		require.Equal(t, videoFeeSubmissionKeyPrefix+row.TaskID, r.Header.Get("Idempotency-Key"))
		o := model.VideoFeeSubmissionObservation{Object: "submission", Key: r.Header.Get("Idempotency-Key"), Acceptance: "accepted", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), TaskID: &id, Task: &model.VideoFeeTaskObservation{ID: id, Status: providerStatus}}
		body, e := common.Marshal(o)
		require.NoError(t, e)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
	})}
	require.NoError(t, ReconcileVideoFeeSubmissions())
	require.Equal(t, 1, calls)
	var task model.Task
	require.NoError(t, db.Where("task_id = ?", row.TaskID).First(&task).Error)
	require.True(t, task.PrivateData.ProviderObservationOnly)
	var user model.User
	require.NoError(t, db.First(&user, 7).Error)
	require.Equal(t, 7000, user.Quota)
	// Customer GET/replay and another immediate worker pass never re-send generation.
	require.NoError(t, ReconcileVideoFeeSubmissions())
	require.Equal(t, 1, calls)
	var saved model.VideoSubmission
	require.NoError(t, db.First(&saved, "id = ?", row.ID).Error)
	require.True(t, saved.TaskReady)
	require.Equal(t, model.VideoSubmissionAccepted, saved.Status)
	// A unique active original is refreshed with GET only. Its response is not
	// substituted for the next atomic by-key state/billing snapshot.
	providerStatus = "processing"
	require.NoError(t, db.Model(&row).UpdateColumns(map[string]any{"provider_lookup_next": 0, "updated_at": time.Now().Add(-time.Minute).Unix()}).Error)
	require.NoError(t, ReconcileVideoFeeSubmissions())
	require.Equal(t, 3, calls)
	require.Equal(t, 1, polls)
	require.NoError(t, db.First(&task, task.ID).Error)
	require.EqualValues(t, model.TaskStatusInProgress, task.Status)
	// A stored success is readable through the original authenticated content
	// route even if the provider has not warmed its local delivery cache.
	providerStatus = "succeeded"
	require.NoError(t, db.Model(&row).UpdateColumns(map[string]any{"provider_lookup_next": 0, "updated_at": time.Now().Add(-time.Minute).Unix()}).Error)
	require.NoError(t, ReconcileVideoFeeSubmissions())
	require.Equal(t, 4, calls)
	require.Equal(t, 1, polls)
	require.NoError(t, db.First(&task, task.ID).Error)
	require.EqualValues(t, model.TaskStatusSuccess, task.Status)
	require.Equal(t, "https://seedance2026.vip/v1/videos/"+id+"/content", task.PrivateData.ResultURL)
	// Neither ordinary polling nor financial entry points may touch the original.
	require.NoError(t, updateVideoSingleTask(context.Background(), nil, nil, row.TaskID, map[string]*model.Task{row.TaskID: &task}))
	// Financial entry points also fail closed if accidentally called elsewhere.
	RefundTaskQuota(context.Background(), &task, "late duplicate failure")
	RecalculateTaskQuota(context.Background(), &task, 900, "late duplicate settlement")
	RecalculateTaskQuotaByTokens(context.Background(), &task, 100000)
	require.NoError(t, db.First(&user, 7).Error)
	require.Equal(t, 7000, user.Quota)
}
