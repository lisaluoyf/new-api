package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

func TestBeeNexQueryEnvelopeClassification(t *testing.T) {
	cases := []struct{ name, body, kind string }{
		{"flat", `{"id":"native-query","status":"in_progress"}`, ""},
		{"legacy", `{"code":"success","data":{"id":42,"task_id":"native-query","status":"IN_PROGRESS"}}`, ""},
		{"terminal_failure", `{"id":"native-query","status":"failed","error":{"code":"ModerationRejected","message":"review failed"}}`, ""},
		{"nested_terminal_failure", `{"code":"success","data":{"task_id":"native-query","status":"FAILED","error":{"code":"ModerationRejected"}}}`, ""},
		{"nested_query_error", `{"data":{"id":"native-query","status":"running","error":{"code":"QueryUnavailable"}}}`, "upstream_error"},
		{"operation_failure", `{"code":"fail_to_fetch_task","message":"signed media https://secret.example/key"}`, "upstream_error"},
		{"unknown", `{"id":"native-query","status":"UPLOAD_STAGE"}`, "unknown_status"},
		{"empty", `{"id":"native-query","progress":50}`, "missing_status"},
		{"no_id", `{"status":"running"}`, "missing_task_id"},
		{"conflicting_ids", `{"id":"different-query","task_id":"native-query","status":"running"}`, "identity_mismatch"},
		{"html", `<html>private-secret</html>`, "invalid_json"},
		{"secret_status", `{"id":"native-query","status":"private-secret"}`, "unknown_status"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeBeeNexTaskResponse([]byte(tc.body), "private-secret")
			if tc.kind == "" {
				require.NoError(t, err)
				return
			}
			var detail *BeeNexTaskQueryError
			require.ErrorAs(t, err, &detail)
			require.Equal(t, tc.kind, detail.Kind)
			require.NotContains(t, detail.Error(), "private-secret")
			require.NotContains(t, detail.Error(), "secret.example")
			if tc.name == "unknown" {
				require.Contains(t, BeeNexTaskQueryFailureSummary(err), "UPLOAD_STAGE")
			}
		})
	}
}

func queryFixtureParser(raw []byte) (*relaycommon.TaskInfo, error) {
	d, err := DecodeBeeNexTaskResponse(raw, "fixture-key")
	if err != nil {
		return nil, err
	}
	id := d.Get("task_id").String()
	if id == "" {
		id = d.Get("id").String()
	}
	return &relaycommon.TaskInfo{TaskID: id, Status: strings.ToUpper(d.Get("status").String())}, nil
}

func TestBeeNexReadQueryFallbackAndFailClosed(t *testing.T) {
	for _, scenario := range []string{"recover_nested", "recover_html", "all_unknown", "identity", "auth", "backoff", "redirect"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			paths := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				paths = append(paths, r.URL.Path)
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "Bearer fixture-key", r.Header.Get("Authorization"))
				w.Header().Set("Content-Type", "application/json")
				switch scenario {
				case "auth":
					w.WriteHeader(403)
				case "redirect":
					w.Header().Set("Location", "/trap")
					w.WriteHeader(302)
				case "backoff":
					w.Header().Set("Retry-After", "10")
					w.WriteHeader(429)
				case "identity":
					fmt.Fprint(w, `{"id":"another-task","status":"running"}`)
				case "all_unknown":
					fmt.Fprint(w, `{"id":"native-query","status":"UPLOAD_STAGE"}`)
				default:
					if calls == 1 {
						if scenario == "recover_html" {
							w.Header().Set("Content-Type", "text/html")
							w.WriteHeader(502)
							fmt.Fprint(w, "<html>unavailable</html>")
						} else {
							fmt.Fprint(w, `{"code":"fail_to_fetch_task","message":"query unavailable"}`)
						}
					} else {
						fmt.Fprint(w, `{"code":"success","data":{"id":42,"task_id":"native-query","status":"IN_PROGRESS"}}`)
					}
				}
			}))
			defer server.Close()
			resp, err := QueryBeeNexTask(context.Background(), server.Client(), server.URL, "fixture-key", "native-query", "sora", queryFixtureParser)
			if strings.HasPrefix(scenario, "recover") {
				require.NoError(t, err)
				defer resp.Body.Close()
				require.Equal(t, 2, calls)
				require.Equal(t, []string{"/v1/videos/native-query", "/v1/video/generations/native-query"}, paths)
				require.Equal(t, "video_generations", resp.Header.Get("X-BeeNex-Query-Protocol"))
				return
			}
			require.Error(t, err)
			require.Nil(t, resp)
			if scenario == "all_unknown" {
				require.Equal(t, 3, calls)
			} else {
				require.Equal(t, 1, calls)
			}
			require.NotContains(t, err.Error(), "fixture-key")
		})
	}
}

func TestBeeNexQueryCancellationDoesNotRetry(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); <-r.Context().Done() }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	_, err := QueryBeeNexTask(ctx, server.Client(), server.URL, "fixture-key", "native-query", "sora", queryFixtureParser)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.EqualValues(t, 1, calls.Load())
}

type beeNexSharedQueryAdaptor struct {
	calls   *atomic.Int32
	entered chan struct{}
	release chan struct{}
	once    *sync.Once
}

func (a beeNexSharedQueryAdaptor) Init(*relaycommon.RelayInfo) {}
func (a beeNexSharedQueryAdaptor) AdjustBillingOnComplete(*model.Task, *relaycommon.TaskInfo) int {
	panic("pending query must not bill")
}
func (a beeNexSharedQueryAdaptor) FetchTask(_ string, key string, _ map[string]any, _ string) (*http.Response, error) {
	if key != "own-stored-key" {
		return nil, errors.New("original credential was not retained")
	}
	a.calls.Add(1)
	a.once.Do(func() { close(a.entered) })
	<-a.release
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"native-query","status":"processing"}`))}, nil
}
func (a beeNexSharedQueryAdaptor) ParseTaskResult([]byte) (*relaycommon.TaskInfo, error) {
	return &relaycommon.TaskInfo{TaskID: "native-query", Status: "IN_PROGRESS", Progress: "25%"}, nil
}

func TestBeeNexConcurrentOwnerQueriesShareLiveRefresh(t *testing.T) {
	db := seedanceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Task{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	base := "https://query.example"
	require.NoError(t, db.Create(&model.Channel{Id: 274, Key: "rotated-key", BaseURL: &base}).Error)
	task := model.Task{TaskID: "task-query-shared", UserId: 88, ChannelId: 274, Platform: constant.TaskPlatformBeeNex, Status: model.TaskStatusQueued, Progress: "0%", Properties: model.Properties{OriginModelName: "seedance-2.5"}, PrivateData: model.TaskPrivateData{UpstreamTaskID: "native-query", Key: "own-stored-key"}}
	require.NoError(t, db.Create(&task).Error)
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	once := new(sync.Once)
	previous := GetTaskAdaptorFunc
	t.Cleanup(func() { GetTaskAdaptorFunc = previous })
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor {
		return beeNexSharedQueryAdaptor{&calls, entered, release, once}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); copy := task; errs <- RefreshBeeNexSeedanceTask(context.Background(), &copy) }()
	}
	<-entered
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, calls.Load())
	var saved model.Task
	require.NoError(t, db.First(&saved, task.ID).Error)
	require.EqualValues(t, "IN_PROGRESS", saved.Status)
	require.Equal(t, "25%", saved.Progress)
	wrongOwner := task
	wrongOwner.UserId = 99
	require.Error(t, RefreshBeeNexSeedanceTask(context.Background(), &wrongOwner))
	require.EqualValues(t, 1, calls.Load())
}

func TestBeeNexQueryPreservesProviderBackoff(t *testing.T) {
	require.Equal(t, 2, BeeNexTaskQueryRetryAfter(errors.New("generic")))
	require.Equal(t, 15, BeeNexTaskQueryRetryAfter(fmt.Errorf("query: %w", &BeeNexTaskQueryError{HTTPStatus: 429, RetryAfter: 15})))
}
