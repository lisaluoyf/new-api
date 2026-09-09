package service

import (
	"context"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestImagineResultShapesAndErrors(t *testing.T) {
	task := &model.ImagineTask{UpstreamID: "test"}
	for _, body := range []string{
		`{"id":"test","status":"completed","cost":0.05504,"result":{"images":[{"url":["https://example.com/1.png","https://example.com/2.png"],"expires_at":123}]}}`,
		`{"data":{"id":"test","status":"completed","result":{"image_urls":["https://example.com/1.png","https://example.com/2.png"]}}}`,
		`{"id":"test","status":"completed","result":{"images":["https://example.com/1.png",{"url":"https://example.com/2.png"}]}}`,
	} {
		result, err := parseImagineResult(task, []byte(body), false)
		require.NoError(t, err)
		var images []string
		require.NoError(t, common.UnmarshalJsonStr(result.Images, &images))
		require.Len(t, images, 2)
	}
	for _, body := range []string{`{}`, `{"id":"other","status":"failed"}`, `{"id":"test","status":"completed"}`, `{"id":"test","status":"completed","progress":101}`} {
		_, err := parseImagineResult(task, []byte(body), false)
		require.Error(t, err)
	}
	result, err := parseImagineResult(task, []byte(`{"id":"test","status":"failed","error":{"code":"422","message":"details","type":"validation_error","param":"prompt"}}`), false)
	require.NoError(t, err)
	require.Equal(t, "422", result.ErrorCode)
	require.Equal(t, "prompt", result.ErrorParam)
}

func TestImagineCallbackValidationAndDurableInbox(t *testing.T) {
	old := model.DB
	oldRedis := common.RedisEnabled
	t.Cleanup(func() { model.DB = old; common.RedisEnabled = oldRedis })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	defer sqlDB.Close()
	model.DB = db
	common.RedisEnabled = false
	require.NoError(t, db.AutoMigrate(&model.ImagineBatch{}, &model.ImagineTask{}, &model.User{}, &model.ImagineBillingEvent{}))
	secret := strings.Repeat("a", 64)
	user := model.User{Username: "callback-test", Quota: 1000000 - 22520}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&model.ImagineBatch{ID: "batch", RequestID: "req", Status: "submitted", CallbackToken: secret, UserID: user.Id, BillingSource: "wallet", ReservedQuota: 22520, UnitQuota: 22520}).Error)
	require.NoError(t, db.Create(&model.ImagineTask{ID: "public", BatchID: "batch", Provider: "apimart", UpstreamID: "up", Status: "queued"}).Error)
	code, _ := SaveImagineCallback(strings.Repeat("b", 64), []byte(`{"id":"up","status":"failed"}`))
	require.Equal(t, 403, code)
	code, _ = SaveImagineCallback(secret, []byte(`{"id":"missing","status":"failed"}`))
	require.Equal(t, 404, code)
	code, _ = SaveImagineCallback(secret, []byte(`{"id":"up","status":"completed"}`))
	require.Equal(t, 400, code)
	for i := 0; i < 3; i++ {
		code, err := SaveImagineCallback(secret, []byte(`{"id":"up","status":"failed","error":{"message":"rejected"}}`))
		require.NoError(t, err)
		require.Equal(t, 200, code)
	}
	var task model.ImagineTask
	require.NoError(t, db.First(&task, "id = ?", "public").Error)
	require.Contains(t, task.CallbackPayload, "rejected")
	require.Equal(t, "queued", task.Status)
	// A racing progress query must not discard a callback that arrived after the query started.
	require.NoError(t, model.ApplyImagineResult(task.ID, model.ImagineTask{Status: "processing", Progress: 40}))
	require.NoError(t, db.First(&task, "id = ?", "public").Error)
	require.Contains(t, task.CallbackPayload, "rejected")
	code, _ = SaveImagineCallback(secret, []byte(`{"id":"up","status":"processing"}`))
	require.Equal(t, 400, code)
	for i := 0; i < 3; i++ {
		require.NoError(t, QueryImagineTask(context.Background(), &task))
		code, err := SaveImagineCallback(secret, []byte(`{"id":"up","status":"failed","error":{"message":"rejected"}}`))
		require.NoError(t, err)
		require.Equal(t, 200, code)
	}
	require.NoError(t, db.First(&task, "id = ?", "public").Error)
	require.Equal(t, "failed", task.Status)
	require.Equal(t, "webhook", task.CompletionSource)
	require.Equal(t, "refunded", task.RefundStatus)
	require.NoError(t, db.First(&user, user.Id).Error)
	require.Equal(t, 1000000, user.Quota)
	var count int64
	require.NoError(t, db.Model(&model.ImagineBillingEvent{}).Count(&count).Error)
	require.Equal(t, int64(1), count)
}

func TestImagineSpeedPercentiles(t *testing.T) {
	stats := ImagineDurationStats([]int64{100, 20, 30, 40, 50})
	require.Equal(t, int64(40), stats["p50"])
	require.Equal(t, int64(100), stats["p95"])
	require.Equal(t, float64(48), stats["average"])
}
