package relay

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestBeeNexPublicQueryFailureDoesNotReturnCachedProgress(t *testing.T) {
	oldDB := model.DB
	oldFactory := service.GetTaskAdaptorFunc
	t.Cleanup(func() { model.DB = oldDB; service.GetTaskAdaptorFunc = oldFactory })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Channel{}, &model.VideoVerificationRun{}))
	base := "https://beenexapi.phone580.com"
	require.NoError(t, db.Create(&model.Channel{Id: 274, Type: 1, BaseURL: &base}).Error)
	task := model.Task{TaskID: "task_existing", UserId: 1, ChannelId: 274, Platform: "beenex", Status: model.TaskStatusInProgress, Progress: "50%", Properties: model.Properties{OriginModelName: "seedance-2.5"}, PrivateData: model.TaskPrivateData{UpstreamTaskID: "supplier-existing"}}
	require.NoError(t, db.Create(&task).Error)
	service.GetTaskAdaptorFunc = nil // An unavailable query path must surface as 502.
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("id", 1)
	c.Params = gin.Params{{Key: "task_id", Value: task.TaskID}}
	c.Request = httptest.NewRequest("GET", "/v1/videos/"+task.TaskID, nil)
	body, taskErr := videoFetchByIDRespBodyBuilder(c)
	require.Nil(t, body)
	require.NotNil(t, taskErr)
	require.Equal(t, http.StatusBadGateway, taskErr.StatusCode)
	require.Equal(t, "task_query_unavailable", taskErr.Code)
	require.Equal(t, 274, c.GetInt("channel_id"))
	require.Equal(t, task.TaskID, c.GetString("video_query_task_id"))
	require.Equal(t, "no-store", c.Writer.Header().Get("Cache-Control"))
	require.Equal(t, "2", c.Writer.Header().Get("Retry-After"))
	var unchanged model.Task
	require.NoError(t, db.First(&unchanged, task.ID).Error)
	require.Equal(t, "50%", unchanged.Progress)
	require.EqualValues(t, "IN_PROGRESS", unchanged.Status)
}
