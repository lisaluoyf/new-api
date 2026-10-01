package controller

import (
	"fmt"
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

func TestSeedanceLastFrameOwnershipAndUnavailableMedia(t *testing.T) {
	service.InitHttpClient()
	oldDB, oldMemory := model.DB, common.MemoryCacheEnabled
	db, e := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, e)
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Channel{}, &model.User{}))
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
	hits := 0
	remoteStatus := 200
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		require.Empty(t, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(remoteStatus)
		fmt.Fprint(w, "image fixture")
	}))
	defer source.Close()
	require.NoError(t, db.Create(&model.Channel{Id: 91, Type: 1, Key: "private-secret", BaseURL: &source.URL}).Error)
	require.NoError(t, db.Create(&model.User{Id: 2, Role: 1}).Error)
	task := model.Task{TaskID: "task_public", UserId: 1, ChannelId: 91, Status: model.TaskStatusSuccess, Properties: model.Properties{OriginModelName: "seedance-2.5"}, PrivateData: model.TaskPrivateData{SeedanceRequest: map[string]any{"return_last_frame": true}}, Data: []byte(fmt.Sprintf(`{"data":{"result":{"videos":[{"last_frame_url":%q}]}}}`, source.URL+"/frame.png"))}
	require.NoError(t, db.Create(&task).Error)
	invoke := func(user int) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/v1/videos/task_public/last-frame", nil)
		c.Set("id", user)
		c.Params = gin.Params{{Key: "task_id", Value: task.TaskID}}
		VideoLastFrame(c)
		return w
	}
	w := invoke(2)
	require.Equal(t, 404, w.Code)
	require.Zero(t, hits)
	w = invoke(1)
	require.Equal(t, 200, w.Code)
	require.Equal(t, "image/png", w.Header().Get("Content-Type"))
	require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
	require.Equal(t, 1, hits)
	remoteStatus = 404
	w = invoke(1)
	require.Equal(t, 410, w.Code)
	require.Contains(t, w.Body.String(), "Media has expired")
	require.NotContains(t, w.Body.String(), source.URL)
	task.PrivateData.SeedanceRequest["return_last_frame"] = false
	require.NoError(t, db.Model(&task).Update("private_data", task.PrivateData).Error)
	w = invoke(1)
	require.Equal(t, 404, w.Code)
	require.Equal(t, 2, hits)
}
