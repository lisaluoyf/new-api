package controller

import (
	"bytes"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestBeeNexDeliveryReportsActualMP4AndNoInventedLastFrame(t *testing.T) {
	service.InitHttpClient()
	oldDB, oldMemory := model.DB, common.MemoryCacheEnabled
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Channel{}, &model.User{}, &model.VideoVerificationRun{}))
	model.DB = db
	common.MemoryCacheEnabled = false
	setting := system_setting.GetFetchSetting()
	oldProtection := setting.EnableSSRFProtection
	setting.EnableSSRFProtection = false
	t.Cleanup(func() {
		model.DB = oldDB
		common.MemoryCacheEnabled = oldMemory
		setting.EnableSSRFProtection = oldProtection
	})
	raw := make([]byte, 64)
	binary.BigEndian.PutUint32(raw, 24)
	copy(raw[4:], "ftypisom")
	copy(raw[16:], "isommp42")
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "video/quicktime")
		w.Write(raw)
	}))
	defer upstream.Close()
	require.NoError(t, db.Create(&model.Channel{Id: 274, Type: 1, Key: "own-key", BaseURL: &upstream.URL}).Error)
	task := model.Task{TaskID: "task_original_mov_request", UserId: 1, ChannelId: 274, Status: model.TaskStatusSuccess, Properties: model.Properties{OriginModelName: "seedance-2.5"}, PrivateData: model.TaskPrivateData{UpstreamVideoURL: upstream.URL + "/result.mp4?private_signature=test", SeedanceRequest: map[string]any{"output_format": "mov", "return_last_frame": true, "seed": float64(0)}}}
	require.NoError(t, db.Create(&task).Error)
	invoke := func(last bool, user int) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("id", user)
		c.Request = httptest.NewRequest("GET", "/v1/videos/"+task.TaskID+"/content", nil)
		c.Params = gin.Params{{Key: "task_id", Value: task.TaskID}}
		if last {
			VideoLastFrame(c)
		} else {
			VideoProxy(c)
		}
		return w
	}
	w := invoke(false, 1)
	require.Equal(t, 200, w.Code)
	require.Equal(t, "video/mp4", w.Header().Get("Content-Type"))
	require.Contains(t, w.Header().Get("Content-Disposition"), ".mp4")
	require.True(t, bytes.Equal(raw, w.Body.Bytes()))
	require.Equal(t, 1, calls)
	w = invoke(true, 1)
	require.Equal(t, http.StatusNotImplemented, w.Code)
	require.Contains(t, w.Body.String(), "last_frame_unsupported")
	require.Equal(t, 1, calls)
	w = invoke(false, 2)
	require.Equal(t, 404, w.Code)
	require.Equal(t, 1, calls)
	// A MOV (qt) or unrelated container is never labeled as MP4.
	copy(raw[8:12], "qt  ")
	w = invoke(false, 1)
	require.Equal(t, 502, w.Code)
	require.NotContains(t, w.Body.String(), "private_signature")
}
