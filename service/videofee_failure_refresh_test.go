package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func resetVideoFailureRefreshCache(t *testing.T) {
	t.Helper()
	videoFailureRefreshCache.Lock()
	old := videoFailureRefreshCache.entries
	videoFailureRefreshCache.entries = make(map[string]*videoFailureRefreshEntry)
	videoFailureRefreshCache.Unlock()
	t.Cleanup(func() {
		videoFailureRefreshCache.Lock()
		videoFailureRefreshCache.entries = old
		videoFailureRefreshCache.Unlock()
	})
}

func TestVideoFeeFailureReadRefreshCoalescesWithoutChangingStoredTask(t *testing.T) {
	db := seedanceTestDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.Task{}))
	resetVideoFailureRefreshCache(t)
	if GetHttpClient() == nil {
		InitHttpClient()
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/v1/video/generations/provider-task" || r.Header.Get("Authorization") != "Bearer supplier-key" {
			w.WriteHeader(400)
			return
		}
		time.Sleep(20 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"success","data":{"task_id":"provider-task","status":"FAILURE","error_code":"upstream_material_api_failed","fail_reason":"Width must be between 300px and 6000px."}}`))
	}))
	defer server.Close()
	base := server.URL
	require.NoError(t, db.Create(&model.Channel{Id: 273, Name: "test-route", BaseURL: &base, Key: "supplier-key"}).Error)
	task := &model.Task{TaskID: "public-task", UserId: 42, Platform: constant.TaskPlatformVideoFee, ChannelId: 273, Status: model.TaskStatusFailure, FailReason: "Video generation failed", FinishTime: 100, Quota: 1234, Properties: model.Properties{OriginModelName: "seedance-2.5"}, PrivateData: model.TaskPrivateData{Key: "supplier-key", UpstreamTaskID: "provider-task"}, Data: []byte(`{"code":"success","data":{"status":"FAILURE","error_code":"TASK_FAILED","fail_reason":""}}`)}
	require.NoError(t, db.Create(task).Error)
	var wg sync.WaitGroup
	results := make(chan *dto.OpenAIVideoError, 8)
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			copy := *task
			RefreshVideoFeeFailureDetails(context.Background(), &copy)
			results <- PublicTaskError(&copy)
		}()
	}
	wg.Wait()
	close(results)
	for public := range results {
		require.Equal(t, "upstream_material_api_failed", public.Code)
		require.Equal(t, "Width must be between 300px and 6000px.", public.Message)
	}
	require.EqualValues(t, 1, calls.Load())
	var saved model.Task
	require.NoError(t, db.First(&saved, task.ID).Error)
	require.Equal(t, task.Status, saved.Status)
	require.Equal(t, task.FailReason, saved.FailReason)
	require.Equal(t, task.Data, saved.Data)
	require.Equal(t, task.FinishTime, saved.FinishTime)
	require.Equal(t, task.Quota, saved.Quota)
	require.Equal(t, task.PrivateData, saved.PrivateData)
}

func TestVideoFeeFailureRefreshRejectsOtherTasksAndNonFailure(t *testing.T) {
	db := seedanceTestDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	resetVideoFailureRefreshCache(t)
	if GetHttpClient() == nil {
		InitHttpClient()
	}
	var calls atomic.Int32
	body := `{"data":{"task_id":"different-task","status":"FAILURE","error_code":"InvalidParameter","fail_reason":"Wrong task detail"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _, _ = w.Write([]byte(body)) }))
	defer server.Close()
	base := server.URL
	require.NoError(t, db.Create(&model.Channel{Id: 273, BaseURL: &base, Key: "supplier-key"}).Error)
	task := &model.Task{TaskID: "public-other", Platform: constant.TaskPlatformVideoFee, ChannelId: 273, Status: model.TaskStatusFailure, FailReason: "Video generation failed", Properties: model.Properties{OriginModelName: "seedance-2.5"}, PrivateData: model.TaskPrivateData{UpstreamTaskID: "provider-task"}}
	RefreshVideoFeeFailureDetails(context.Background(), task)
	require.Equal(t, "Video generation failed", task.FailReason)
	RefreshVideoFeeFailureDetails(context.Background(), task)
	require.EqualValues(t, 1, calls.Load(), "empty lookups are cached too")
	for _, status := range []model.TaskStatus{model.TaskStatusSuccess, model.TaskStatusInProgress} {
		copy := *task
		copy.Status = status
		RefreshVideoFeeFailureDetails(context.Background(), &copy)
	}
	copy := *task
	copy.FailReason = "Specific existing error"
	RefreshVideoFeeFailureDetails(context.Background(), &copy)
	require.EqualValues(t, 1, calls.Load())
}
