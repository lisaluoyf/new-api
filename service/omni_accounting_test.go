package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestOmniAccountingUsesActualCostAndNetLedger(t *testing.T) {
	oldDB, oldLog := model.DB, model.LOG_DB
	t.Cleanup(func() { model.DB, model.LOG_DB = oldDB, oldLog })
	db, e := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, e)
	model.DB, model.LOG_DB = db, db
	require.NoError(t, db.AutoMigrate(&model.Log{}))

	row := model.Log{UserId: 9, Type: model.LogTypeConsume, ChannelId: 400, ModelName: "gemini-omni-1.1-flash", Quota: 550000, Other: `{"task_id":"task_omni","is_task":true}`, AccountingStatus: "partial", AccountingSnapshot: `{"status":"partial","error":"roma_user_price_missing","amounts_usd":{"channel_cost":1,"user_final":1.1}}`}
	require.NoError(t, db.Create(&row).Error)
	adjust := model.Log{UserId: 9, Type: model.LogTypeConsume, ModelName: row.ModelName, Other: `{"task_id":"task_omni","actual_quota":330000}`, Quota: 100}
	require.NoError(t, db.Create(&adjust).Error)
	task := &model.Task{TaskID: "task_omni", Platform: "apimart-omni", UserId: 9, ChannelId: 400, Status: model.TaskStatusSuccess, Quota: 330000, Data: []byte(`{"data":{"cost":0.6,"duration":6}}`), PrivateData: model.TaskPrivateData{ResultURL: "https://example.com/video.mp4", BillingContext: &model.TaskBillingContext{ModelPrice: .0968, GroupRatio: 1}}}
	for range 2 {
		require.NoError(t, SyncOmniTaskAccounting(task, &relaycommon.TaskInfo{}))
	}
	var got model.Log
	require.NoError(t, db.First(&got, row.Id).Error)
	require.Equal(t, "ok", got.AccountingStatus)
	require.InDelta(t, .6, got.AccountingChannelCostAmountUSD, 1e-10)
	require.InDelta(t, .66, got.AccountingUserFinalAmountUSD, 1e-10)
	require.Equal(t, 550000, got.Quota)
	var other map[string]any
	require.NoError(t, common.UnmarshalJsonStr(got.Other, &other))
	require.Equal(t, float64(6), other["output_video_seconds"])
	require.Equal(t, "https://example.com/video.mp4", other["result_url"])
	var untouched model.Log
	require.NoError(t, db.First(&untouched, adjust.Id).Error)
	require.Equal(t, 100, untouched.Quota)
	require.Equal(t, adjust.Other, untouched.Other)
}
