package relay

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http/httptest"
	"testing"
)

func TestVeoRemixInitializesChannelMetadataBeforeLockingOrigin(t *testing.T) {
	oldDB, oldRedis := model.DB, common.RedisEnabled
	t.Cleanup(func() { model.DB, common.RedisEnabled = oldDB, oldRedis })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB, common.RedisEnabled = db, false
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Task{}))
	base := "https://api.apib.ai"
	require.NoError(t, db.Create(&model.Channel{Id: 306, Type: constant.ChannelTypeOpenAI, Name: "Veo Ext", Key: "origin-key", BaseURL: &base, Status: common.ChannelStatusEnabled, Models: "veo-3.1-fast-generate-preview"}).Error)
	require.NoError(t, db.Create(&model.Task{TaskID: "task_origin", UserId: 42, ChannelId: 306, Status: model.TaskStatusSuccess, Properties: model.Properties{OriginModelName: "veo-3.1-fast-generate-preview"}, PrivateData: model.TaskPrivateData{BillingContext: &model.TaskBillingContext{OtherRatios: map[string]float64{"seconds": 8, "size": 3}}}}).Error)
	for _, selected := range []int{305, 306} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/videos/task_origin/remix", nil)
		c.Params = gin.Params{{Key: "video_id", Value: "task_origin"}}
		common.SetContextKey(c, constant.ContextKeyChannelId, selected)
		common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
		common.SetContextKey(c, constant.ContextKeyChannelKey, "origin-key")
		common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, base)
		info := &relaycommon.RelayInfo{UserId: 42, TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
		require.Nil(t, info.ChannelMeta)
		require.NotPanics(t, func() { require.Nil(t, ResolveOriginTask(c, info)) })
		require.NotNil(t, info.ChannelMeta)
		require.Equal(t, 306, info.ChannelId)
		require.Equal(t, "task_origin", info.OriginTaskID)
		require.Equal(t, constant.TaskActionRemix, info.Action)
		require.Equal(t, 8.0, info.PriceData.OtherRatios["seconds"])
		require.Equal(t, selected != 306, c.GetBool("task_origin_channel_changed"))
	}
}
