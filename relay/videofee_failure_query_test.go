package relay

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestVideoFeeTaskQueriesRefreshOnlyOwnedGenericFailure(t *testing.T) {
	db := setupProviderTaskTestDB(t)
	if service.GetHttpClient() == nil {
		service.InitHttpClient()
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"code":"success","data":{"task_id":"provider-query","status":"FAILURE","error_code":"upstream_material_api_failed","fail_reason":"Width must be between 300px and 6000px."}}`))
	}))
	defer server.Close()
	base := server.URL
	require.NoError(t, db.Create(&model.Channel{Id: 273, Type: constant.ChannelTypeOpenAI, BaseURL: &base, Key: "provider-query-key"}).Error)
	task := model.Task{TaskID: "task_late_query_details", UserId: 42, ChannelId: 273, Platform: constant.TaskPlatformVideoFee, Status: model.TaskStatusFailure, FailReason: "Video generation failed", Properties: model.Properties{OriginModelName: "seedance-2.5"}, PrivateData: model.TaskPrivateData{UpstreamTaskID: "provider-query"}, Data: []byte(`{"data":{"status":"FAILURE","error_code":"TASK_FAILED","fail_reason":""}}`)}
	require.NoError(t, db.Create(&task).Error)
	for _, user := range []int{7, 42} {
		for _, path := range []string{"/v1/videos/", "/v1/video/generations/"} {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set("id", user)
			c.Params = gin.Params{{Key: "task_id", Value: task.TaskID}}
			c.Request = httptest.NewRequest(http.MethodGet, path+task.TaskID, nil)
			body, taskErr := videoFetchByIDRespBodyBuilder(c)
			if user != 42 {
				require.NotNil(t, taskErr)
				require.Nil(t, body)
				require.Zero(t, calls.Load())
				continue
			}
			require.Nil(t, taskErr)
			var out map[string]any
			require.NoError(t, common.Unmarshal(body, &out))
			if data, ok := out["data"].(map[string]any); ok {
				out = data
			}
			require.Equal(t, "failed", out["status"])
			err := out["error"].(map[string]any)
			require.Equal(t, "upstream_material_api_failed", err["code"])
			require.Equal(t, "Width must be between 300px and 6000px.", err["message"])
		}
	}
	require.EqualValues(t, 1, calls.Load())
	var stored model.Task
	require.NoError(t, db.First(&stored, task.ID).Error)
	require.Equal(t, "upstream_material_api_failed", service.PublicTaskError(&stored).Code)
	require.Equal(t, "Width must be between 300px and 6000px.", service.PublicTaskFailure(&stored))
	require.Equal(t, task.Data, stored.Data)
}
