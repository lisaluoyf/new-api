package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

func TestImageReconcilePersistsTaskPreview(t *testing.T) {
	truncate(t)
	user := &model.User{Id: 901, Username: "image_reconcile", Quota: 1000000, Status: common.UserStatusEnabled}
	require.NoError(t, model.DB.Create(user).Error)
	require.NoError(t, model.DB.Create(&model.Channel{Id: 81}).Error)

	const upstreamURL = "https://example.com/task-image.png"
	const cachedURL = "https://apimaster.ai/imgs/exact-task.png"
	oldCache := cacheImageLocallyImpl
	t.Cleanup(func() { cacheImageLocallyImpl = oldCache })
	cacheCalls := 0
	cacheImageLocallyImpl = func(url string, headers imageCacheHeaders) string {
		require.Equal(t, upstreamURL, url)
		require.Equal(t, "Bearer test-key", headers["Authorization"])
		cacheCalls++
		return cachedURL
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/tasks/task-reconcile", r.URL.Path)
		require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"status":"completed","result":{"images":[{"url":["https://example.com/task-image.png"]}]}}}`))
	}))
	defer server.Close()

	job := imageReconcileJob{
		taskID: "task-reconcile", baseURL: server.URL, apiKey: "test-key",
		requestID: "request-reconcile", startedAt: time.Now(),
		requestData: map[string]interface{}{"model": "gpt-image-2.5-flare", "prompt": "original prompt", "n": 1, "resolution": "2k"},
		relayInfo: &relaycommon.RelayInfo{
			UserId: user.Id, OriginModelName: "gpt-image-2.5-flare", StartTime: time.Now(),
			ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 81},
			Request:     &dto.ImageRequest{Model: "gpt-image-2.5-ext", Prompt: "converted prompt"},
			PriceData:   types.PriceData{UsePrice: true, ModelPrice: 0.25, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}},
		},
	}
	runImageTaskReconcile(job)

	var log model.Log
	require.NoError(t, model.LOG_DB.Where("request_id = ? AND type = ?", job.requestID, model.LogTypeConsume).First(&log).Error)
	var other map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
	require.Equal(t, job.taskID, other["task_id"])
	require.Equal(t, cachedURL, other["result_url"])
	request := other["request_data"].(map[string]interface{})
	require.Equal(t, "original prompt", request["prompt"])
	require.Equal(t, "gpt-image-2.5-flare", request["model"])
	require.Equal(t, "2k", request["resolution"])
	require.Equal(t, 1, cacheCalls)
	require.Contains(t, log.Content, job.taskID)
}

func TestGrokReconcileSettlesActualImagesAndPreviews(t *testing.T) {
	truncate(t)
	user := &model.User{Id: 902, Username: "grok_reconcile", Quota: 1000000, Status: common.UserStatusEnabled}
	require.NoError(t, model.DB.Create(user).Error)
	require.NoError(t, model.DB.Create(&model.Channel{Id: 255}).Error)
	old := cacheImageLocallyImpl
	t.Cleanup(func() { cacheImageLocallyImpl = old })
	cacheImageLocallyImpl = func(url string, headers imageCacheHeaders) string { return url }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"status":"completed","result":{"images":[{"url":["https://apimaster.ai/imgs/one.png","https://apimaster.ai/imgs/two.png"]}]}}}`))
	}))
	defer server.Close()
	req := &dto.ImageRequest{Model: dto.GrokImage20Model, N: common.GetPointer(uint(3)), Resolution: "2k", Quality: "medium", ImageUrls: []string{"ref"}}
	// Snapshot remains authoritative if the admin changes prices while polling.
	common.OptionMapRWMutex.Lock()
	before := common.OptionMap
	common.OptionMap = map[string]string{ratio_setting.ImageModelPricingOption: `{}`}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() { common.OptionMapRWMutex.Lock(); common.OptionMap = before; common.OptionMapRWMutex.Unlock() })
	job := imageReconcileJob{grokPricing: grokTestPricing(), taskID: "grok-reconcile", baseURL: server.URL, apiKey: "test", requestID: "grok-reconcile-request", startedAt: time.Now(),
		relayInfo: &relaycommon.RelayInfo{UserId: user.Id, OriginModelName: dto.GrokImage20Model, StartTime: time.Now(), ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 255}, Request: req,
			PriceData: types.PriceData{UsePrice: true, ModelPrice: .0768, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1.05}}}}
	runImageTaskReconcile(job)
	var log model.Log
	require.NoError(t, model.LOG_DB.Where("request_id = ? AND type = ?", job.requestID, model.LogTypeConsume).First(&log).Error)
	require.InDelta(t, .17*.96*1.05*common.QuotaPerUnit, log.Quota, 1)
	var other map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
	require.Equal(t, []interface{}{"https://apimaster.ai/imgs/one.png", "https://apimaster.ai/imgs/two.png"}, other["result_urls"])
	billing := other["image_billing"].(map[string]interface{})
	require.Equal(t, float64(2), billing["generated_images"])
	require.InDelta(t, .17, billing["base_amount_usd"], 1e-10)
	// A failed task never creates a consume log.
	job.requestID = "grok-failed-request"
	job.taskID = "grok-failed"
	finalizeImageReconcileFailure(job, "upstream failed")
	var count int64
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("request_id = ? AND type = ?", job.requestID, model.LogTypeConsume).Count(&count).Error)
	require.Zero(t, count)
}
