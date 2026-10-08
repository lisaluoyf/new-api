package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func videoFailureDB(t *testing.T) *gorm.DB {
	t.Helper()
	oldDB, oldLogDB := model.DB, model.LOG_DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB, model.LOG_DB = db, db
	t.Cleanup(func() { model.DB, model.LOG_DB = oldDB, oldLogDB })
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Log{}))
	return db
}

func TestAdminLogsShowHistoricalVideoFailure(t *testing.T) {
	db := videoFailureDB(t)
	task := model.Task{TaskID: "task_failed", UserId: 42, Status: model.TaskStatusFailure,
		Properties: model.Properties{OriginModelName: "seedance-2.0-mini"},
		FailReason: "Your request did not pass content safety review. Bearer private-secret"}
	require.NoError(t, db.Create(&task).Error)
	log := model.Log{UserId: 42, Type: model.LogTypeConsume, ModelName: "seedance-2.0-mini",
		RequestId: "request_failed", Content: "操作 generate", Other: `{"task_id":"task_failed","billing_refunded":true}`}
	require.NoError(t, db.Create(&log).Error)
	r := gin.New()
	r.GET("/logs", GetAllLogs)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/logs?request_id=request_failed", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "content safety review")
	require.Contains(t, w.Body.String(), "task_fail_reason")
	require.NotContains(t, w.Body.String(), "private-secret")
	var stored model.Log
	require.NoError(t, db.First(&stored, log.Id).Error)
	require.Equal(t, log.Content, stored.Content)
	require.Equal(t, log.Other, stored.Other)
}

func TestVideoContentDistinguishesFailureFromPending(t *testing.T) {
	db := videoFailureDB(t)
	for _, status := range []model.TaskStatus{model.TaskStatusFailure, model.TaskStatusInProgress} {
		t.Run(string(status), func(t *testing.T) {
			task := model.Task{TaskID: "task_" + string(status), UserId: 42, Status: status,
				Platform:   constant.TaskPlatformApimartVideo,
				FailReason: "Your request did not pass content safety review. Bearer private-secret"}
			require.NoError(t, db.Create(&task).Error)
			r := gin.New()
			r.GET("/v1/videos/:task_id/content", func(c *gin.Context) { c.Set("id", 42); VideoProxy(c) })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/videos/"+task.TaskID+"/content", nil))
			require.Equal(t, http.StatusBadRequest, w.Code)
			var response map[string]any
			require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
			if status == model.TaskStatusFailure {
				require.Equal(t, "failed", response["status"])
				require.Equal(t, "task_failed", response["error"].(map[string]any)["code"])
				require.Contains(t, w.Body.String(), "content safety review")
				require.NotContains(t, w.Body.String(), "not completed yet")
				require.NotContains(t, w.Body.String(), "private-secret")
			} else {
				require.Contains(t, w.Body.String(), "not completed yet")
				require.NotContains(t, w.Body.String(), "content safety review")
			}
		})
	}
}
