package controller

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPublicUpstreamErrorMatchesAPIReceiptAndCustomerLog(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedis })
	oldDB, oldLogDB, oldEnabled := model.DB, model.LOG_DB, constant.ErrorLogEnabled
	model.DB, model.LOG_DB, constant.ErrorLogEnabled = db, db, true
	t.Cleanup(func() {
		model.DB, model.LOG_DB, constant.ErrorLogEnabled = oldDB, oldLogDB, oldEnabled
		sqlDB, _ := db.DB()
		sqlDB.Close()
	})
	require.NoError(t, db.AutoMigrate(&model.VideoSubmission{}, &model.Log{}, &model.User{}, &model.Channel{}))
	require.NoError(t, db.Create(&model.User{Id: 8181, Username: "error-observation-test", Setting: `{}`}).Error)
	baseURL := "https://api.hidden-route.ai"
	require.NoError(t, db.Create(&model.Channel{Id: 91910, Name: "HiddenRelay-video", Key: "private-secret", BaseURL: &baseURL}).Error)
	row := &model.VideoSubmission{ID: "receipt-test", OwnerToken: "test-owner", TaskID: "task_submission_test", UserID: 8181, ChannelID: 91910, Status: model.VideoSubmissionUnknown}
	require.NoError(t, db.Create(row).Error)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/videos", nil)
	c.Set("id", 8181)
	c.Set("token_id", 1919)
	c.Set("original_model", "future-video-model")
	c.Set("channel_id", 91910)
	c.Set("channel_name", "HiddenRelay-video")
	c.Set("base_url", baseURL)
	c.Set("public_error_private_model", "private-deployment-7")
	c.Set(common.RequestIdKey, "gateway-request-test")
	c.Set(service.VideoSubmissionContextKey, row)
	message := "HiddenRelay via api.hidden-route.ai using private-deployment-7: " + strings.Repeat("full diagnostic ", 100) + " diagnostic-end"
	raw, err := common.Marshal(map[string]any{"provider": "HiddenRelay", "model": "private-deployment-7", "error": map[string]any{"code": "HiddenRelay.ValidationFailed", "message": message, "details": map[string]any{"expected": "image/jpeg", "actual": "video/mp4", "api_key": "private-secret"}}})
	require.NoError(t, err)
	failure := service.PublicVideoSubmitError(400, raw, "private-secret", 91910)
	require.True(t, failure.LocalError, "request rejection must not change health attribution")
	service.RecordVideoSubmissionFailure(c, failure)
	respondTaskError(c, failure)
	require.Equal(t, 400, w.Code)
	var api map[string]any
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &api))
	require.Contains(t, w.Body.String(), "diagnostic-end")
	require.NotContains(t, w.Body.String(), "private-secret")
	for _, name := range []string{"HiddenRelay", "hidden-route.ai", "private-deployment-7"} {
		require.NotContains(t, w.Body.String(), name)
	}
	var stored model.VideoSubmission
	require.NoError(t, db.First(&stored, "id = ?", row.ID).Error)
	require.Equal(t, model.VideoSubmissionUnknown, stored.Status, "observing error must not change acceptance or refunds")
	receipt := service.VideoSubmissionPublicError(&stored)
	expected, _ := common.Marshal(failure.UpstreamError)
	actual, _ := common.Marshal(receipt["upstream_error"])
	require.JSONEq(t, string(expected), string(actual))
	require.Contains(t, stored.ErrorDetails, "diagnostic-end")
	for _, name := range []string{"HiddenRelay", "hidden-route.ai", "private-deployment-7"} {
		require.NotContains(t, stored.ErrorDetails, name)
	}
	var logs []model.Log
	require.NoError(t, db.Where("user_id = ? AND type = ?", 8181, model.LogTypeError).Find(&logs).Error)
	require.Len(t, logs, 1, "local upstream errors must leave a customer error log")
	require.Contains(t, logs[0].Content, "diagnostic-end")
	require.NotContains(t, logs[0].Content, "private-secret")
	for _, name := range []string{"HiddenRelay", "hidden-route.ai", "private-deployment-7"} {
		require.NotContains(t, logs[0].Content, name)
	}
	other, _ := common.StrToMap(logs[0].Other)
	logDetail, _ := common.Marshal(other["upstream_error"])
	require.JSONEq(t, string(expected), string(logDetail))
	service.RecordPublicTaskErrorLog(c, failure)
	var count int64
	require.NoError(t, db.Model(&model.Log{}).Where("user_id = ? AND type = ?", 8181, model.LogTypeError).Count(&count).Error)
	require.EqualValues(t, 1, count, "finalizer must not duplicate a previously recorded error")
}
