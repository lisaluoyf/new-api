package relay

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSeedanceQuoteValidatesAllModelsWithoutCreatingTasksOrCharging(t *testing.T) {
	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.User{}, &model.SeedanceBillingReceipt{}))
	require.NoError(t, db.Create(&model.User{Id: 91, Username: "quote-owner", Quota: 1000000}).Error)
	ratio_setting.InitRatioSettings()
	for _, name := range []string{"seedance-2.0", "seedance-2.5", "seedance-2.0-fast", "seedance-2.0-mini"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/videos/quote", strings.NewReader(`{"model":"`+name+`","prompt":"scene","duration":4,"resolution":"480p"}`))
		c.Request.Header.Set("Content-Type", "application/json")
		common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
		common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, "https://api.apib.ai")
		c.Set("id", 91)
		c.Set("group", "default")
		info := &relaycommon.RelayInfo{OriginModelName: name, UserGroup: "default", UsingGroup: "default", TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
		details, taskErr := QuoteSeedanceVideo(c, info)
		require.Nil(t, taskErr, name)
		require.Equal(t, name, details.Model)
		require.Equal(t, "480P", details.Resolution)
		require.Equal(t, 4, details.EstimatedBillableSeconds)
		require.Equal(t, "estimate", details.SettlementStatus)
		require.Nil(t, info.Billing)
		require.Empty(t, info.PublicTaskID)
		require.Equal(t, "/v1/videos/quote", c.Request.URL.Path)
	}
	var count int64
	require.NoError(t, db.Model(&model.Task{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Model(&model.SeedanceBillingReceipt{}).Count(&count).Error)
	require.Zero(t, count)
	var user model.User
	require.NoError(t, db.First(&user, 91).Error)
	require.Equal(t, 1000000, user.Quota)
}
