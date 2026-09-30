package service

import (
	"context"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/abema/go-mp4"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestVideoOutputDimensionsReadActualMP4Track(t *testing.T) {
	for _, dim := range []videoOutputDimensions{{854, 480}, {1280, 720}, {480, 854}} {
		file, err := os.CreateTemp(t.TempDir(), "spec-*.mp4")
		require.NoError(t, err)
		defer file.Close()
		w := mp4.NewWriter(file)
		for _, typ := range []mp4.BoxType{mp4.BoxTypeMoov(), mp4.BoxTypeTrak(), mp4.BoxTypeTkhd()} {
			_, err = w.StartBox(&mp4.BoxInfo{Type: typ})
			require.NoError(t, err)
		}
		_, err = mp4.Marshal(w, &mp4.Tkhd{Width: uint32(dim.Width) << 16, Height: uint32(dim.Height) << 16}, mp4.Context{})
		require.NoError(t, err)
		for i := 0; i < 3; i++ {
			_, err = w.EndBox()
			require.NoError(t, err)
		}
		_, err = file.Seek(0, 0)
		require.NoError(t, err)
		got, err := readVideoOutputDimensions(file)
		require.NoError(t, err)
		require.Equal(t, dim, got)
	}
}

func TestVideoLogPersistsRequestedSubmittedAndBillingSpecs(t *testing.T) {
	req := &relaycommon.TaskSubmitReq{Model: "seedance-2.5", Duration: 4, Metadata: map[string]interface{}{
		"resolution": "480p", "aspect_ratio": "9:16", "billing_variant": "480p-input", "has_video": true,
		"requested_spec": map[string]interface{}{"resolution": "480P", "ratio": "9:16"},
	}}
	data := BuildVideoRequestDataForLog(req)
	require.Equal(t, "480p", data["resolution"])
	require.Equal(t, "480P", data["effective_resolution"])
	require.Equal(t, "9:16", data["aspect_ratio"])
	require.Equal(t, "480p-input", data["billing_variant"])
	require.Equal(t, "480P", data["requested_spec"].(map[string]interface{})["resolution"])
	require.Equal(t, "480p", data["submitted_spec"].(map[string]interface{})["resolution"])
}

func TestDurationAccountingFreezesActualSelectedUnitPrice(t *testing.T) {
	oldDB, oldOptions := model.DB, common.OptionMap
	t.Cleanup(func() { model.DB = oldDB; common.OptionMap = oldOptions })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.ChannelModelPricing{}, &model.User{}))
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "video-accounting"}).Error)
	recharge, markup := 1.0, 1.1
	require.NoError(t, db.Create(&model.Channel{Id: 153, RechargeRate: &recharge, ApimasterPriceRatio: &markup}).Error)
	require.NoError(t, model.UpsertChannelModelPricings([]model.ChannelModelPricing{{ChannelId: 153, ModelName: "seedance-2.0", InputPrice: .14, GroupRatio: 1}}))
	for _, price := range []float64{.1562, .0726} {
		got := BuildConsumeAccountingFields(ConsumeAccountingInput{UserId: 1, ChannelId: 153, ModelName: "seedance-2.0", DurationSeconds: 5, DurationUnitPrice: price, GroupRatio: 1.05, Quota: int(price * 5 * 1.05 * common.QuotaPerUnit)})
		var snap consumeAccountingSnapshot
		require.NoError(t, common.UnmarshalJsonStr(got.Snapshot, &snap))
		user := snap.Prices["user_price"].(map[string]interface{})
		require.InDelta(t, price, user["input_price"], 1e-12)
		require.InDelta(t, float64(got.UserFinalAmountUSD), float64(snap.AmountsUSD["user_final"]), 1e-12)
		require.InDelta(t, price*5*1.05, got.UserFinalAmountUSD, .0000021)
	}
}

func TestMediaWebhookCompletionBackfillsActualVideoDimensions(t *testing.T) {
	old := model.LOG_DB
	t.Cleanup(func() { model.LOG_DB = old })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.LOG_DB = db
	require.NoError(t, db.AutoMigrate(&model.Log{}))
	task := &model.Task{TaskID: "task_webhook_video_dimensions", UserId: 501, Status: model.TaskStatusSuccess, SubmitTime: 10, FinishTime: 30, Properties: model.Properties{OriginModelName: "seedance-2.5"}}
	task.SetData(map[string]interface{}{"width": 480, "height": 854})
	task.PrivateData.ResultURL = "https://apimaster.ai/video/result.mp4"
	require.NoError(t, db.Create(&model.Log{UserId: task.UserId, Type: model.LogTypeConsume, Quota: 221944, Other: common.MapToJsonStr(map[string]interface{}{"task_id": task.TaskID})}).Error)
	backfillMediaTaskWebhookLog(context.Background(), task)
	var log model.Log
	require.NoError(t, db.First(&log).Error)
	var other map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
	require.Equal(t, "verified", other["output_spec_status"])
	require.Equal(t, float64(480), other["output_spec"].(map[string]interface{})["width"])
	require.Equal(t, float64(854), other["output_spec"].(map[string]interface{})["height"])
	require.Equal(t, 221944, log.Quota)
	require.Equal(t, 20, log.UseTime)
	require.Equal(t, task.PrivateData.ResultURL, other["result_url"])
}
