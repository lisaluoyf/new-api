package service

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSeedanceTariffFrozenAcrossChannels(t *testing.T) {
	for _, name := range []string{"seedance-2.0", "doubao-seedance-2.0", "seedance-2.5", "seedance-2.0-fast", "seedance-2.0-mini"} {
		for _, channel := range []int{153, 247, 999} {
			task := &model.Task{ChannelId: channel, Quota: 189000, Properties: model.Properties{OriginModelName: name},
				Data: []byte(`{"duration":5,"cost":900,"usage":{"total_tokens":8000000}}`),
				PrivateData: model.TaskPrivateData{SeedanceRequest: map[string]any{"duration": 5, "video_input_seconds": 7},
					BillingContext: &model.TaskBillingContext{SeedanceTariff: true, OriginModelName: name, ModelPrice: 0.1, GroupRatio: 1.05, OtherRatios: map[string]float64{"seconds": 12, "size": .3}}}}
			require.Equal(t, 12, SeedanceTariffSeconds(task, nil))
			require.Equal(t, 189000, SeedanceTariffQuota(task, nil))
			// Completion token/cost fallback and even per-call flags cannot change this tariff.
			task.PrivateData.BillingContext.PerCallBilling = true
			SettleTaskBillingOnComplete(context.Background(), tariffPanicAdaptor{}, task, &relaycommon.TaskInfo{TotalTokens: 8000000})
			require.Equal(t, 189000, task.Quota)
			task.Data = []byte(`{"duration":6,"cost":0.000001}`)
			require.Equal(t, 13, SeedanceTariffSeconds(task, nil))
			require.Equal(t, 204750, SeedanceTariffQuota(task, nil))
			// Completion uses the frozen price, independent of runtime price changes.
			require.Equal(t, 189000, SeedanceSubmissionQuota(types.PriceData{ModelPrice: .1, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1.05}, OtherRatios: map[string]float64{"seconds": 12, "size": .3}}))
		}
	}
}

type tariffPanicAdaptor struct{}

func (tariffPanicAdaptor) AdjustBillingOnComplete(*model.Task, *relaycommon.TaskInfo) int {
	panic("provider billing must not run")
}

func TestSeedanceTariffMissingDurationAndLegacy(t *testing.T) {
	task := &model.Task{Quota: 50000, PrivateData: model.TaskPrivateData{SeedanceRequest: map[string]any{"duration": 5}, BillingContext: &model.TaskBillingContext{SeedanceTariff: true, OriginModelName: "seedance-2.0", ModelPrice: .02, GroupRatio: 1, OtherRatios: map[string]float64{"seconds": 5, "size": 1}}}, Data: []byte(`{"cost":999}`)}
	require.Equal(t, 50000, SeedanceTariffQuota(task, nil))
	task.PrivateData.BillingContext.SeedanceTariff = false
	require.False(t, UsesSeedanceTariff(task))
	require.Zero(t, SeedanceTariffQuota(task, nil))
	require.Zero(t, SeedanceTariffQuota(nil, nil))
}

func TestSeedanceTariffEstimateIncludesInputWithoutThirtySecondCap(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, name := range []string{"seedance-2.0", "seedance-2.5"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/videos/generations", strings.NewReader(`{"model":"`+name+`","prompt":"scene","video_urls":["https://example.com/ref.mp4"]}`))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("seedance_video_input_seconds", 20)
		c.Set("task_request", relaycommon.TaskSubmitReq{Model: name, Duration: 30, Metadata: map[string]any{"resolution": "720p", "has_video": true, "auto_duration": true}})
		ratios, err := PrepareSeedanceTaskBilling(c, &relaycommon.RelayInfo{OriginModelName: name})
		require.NoError(t, err)
		require.Equal(t, 50.0, ratios["seconds"])
		snapshot, _ := c.Get("seedance_billing_snapshot")
		require.Equal(t, 20, snapshot.(map[string]any)["video_input_seconds"])
		require.Equal(t, "720p-input", snapshot.(map[string]any)["billing_variant"])
	}
}

func TestSeedanceTariffRefusesUnmeasuredInput(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/videos", strings.NewReader(`{}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("task_request", relaycommon.TaskSubmitReq{Model: "seedance-2.0", Duration: 5, Metadata: map[string]any{"resolution": "720p", "has_video": true}})
	_, err := PrepareSeedanceTaskBilling(c, &relaycommon.RelayInfo{OriginModelName: "seedance-2.0"})
	require.ErrorContains(t, err, "verify reference video duration")
	// Native content and OpenAI metadata references both choose the input tariff.
	require.Equal(t, []any{"https://example.com/a.mp4"}, seedanceTariffVideoURLs(map[string]any{"content": []any{map[string]any{"type": "video_url", "video_url": map[string]any{"url": "https://example.com/a.mp4"}}}}))
}

func TestSeedanceTariffWalletDeltaAndFailureRefund(t *testing.T) {
	truncate(t)
	const uid, tid, cid = 8901, 8901, 8901
	seedUser(t, uid, 1_000_000)
	seedToken(t, tid, uid, "sk-seedance-tariff-test", 1_000_000)
	seedChannel(t, cid)
	task := makeTask(uid, cid, 189000, tid, BillingSourceWallet, 0)
	task.PrivateData.BillingContext = &model.TaskBillingContext{SeedanceTariff: true, OriginModelName: "seedance-2.5", ModelPrice: .1, GroupRatio: 1.05, OtherRatios: map[string]float64{"seconds": 12, "size": .3}}
	task.PrivateData.SeedanceRequest = map[string]any{"duration": 5, "video_input_seconds": 7}
	task.Data = []byte(`{"duration":6,"cost":900,"usage":{"total_tokens":8000000}}`)
	SettleTaskBillingOnComplete(context.Background(), tariffPanicAdaptor{}, task, &relaycommon.TaskInfo{TotalTokens: 8000000})
	require.Equal(t, 204750, task.Quota)
	require.Equal(t, 984250, getUserQuota(t, uid))
	require.Equal(t, 984250, getTokenRemainQuota(t, tid))
	// Calling settlement twice must not debit the delta twice.
	SettleTaskBillingOnComplete(context.Background(), tariffPanicAdaptor{}, task, &relaycommon.TaskInfo{TotalTokens: 8000000})
	require.Equal(t, 984250, getUserQuota(t, uid))
	task.Data = []byte(`{"duration":4,"cost":900}`)
	SettleTaskBillingOnComplete(context.Background(), tariffPanicAdaptor{}, task, &relaycommon.TaskInfo{})
	require.Equal(t, 173250, task.Quota)
	require.Equal(t, 1015750, getUserQuota(t, uid))
	RefundTaskQuota(context.Background(), task, "generation failed")
	require.Equal(t, 1189000, getUserQuota(t, uid))
	require.Equal(t, 1189000, getTokenRemainQuota(t, tid))
}
