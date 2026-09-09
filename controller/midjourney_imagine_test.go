package controller

import (
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestImagineSubmissionMapsModelAndChargesActualTasks(t *testing.T) {
	oldDB, oldOptions, oldRedis := model.DB, common.OptionMap, common.RedisEnabled
	oldCallback := operation_setting.CustomCallbackAddress
	t.Cleanup(func() {
		model.DB = oldDB
		common.OptionMap = oldOptions
		common.RedisEnabled = oldRedis
		operation_setting.CustomCallbackAddress = oldCallback
	})
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	defer sqlDB.Close()
	model.DB = db
	common.RedisEnabled = false
	ratio_setting.InitRatioSettings()
	operation_setting.CustomCallbackAddress = "https://apimaster.ai"
	require.NoError(t, db.AutoMigrate(&model.ImagineBatch{}, &model.ImagineTask{}, &model.ImagineBillingEvent{}, &model.User{}, &model.Token{}, &model.Channel{}, &model.ChannelModelPricing{}))
	common.OptionMap = map[string]string{"VideoModelPricing": `{"midjourney-niji-7":{"unit":"generation","base_price":0.04504,"base_variant":"relax","prices":{"relax":0.04504,"fast":0.05504,"turbo":0.1}}}`}
	user := model.User{Username: "imagine", Quota: 1000000}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "test-token", RemainQuota: 1000000}
	require.NoError(t, db.Create(&token).Error)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/midjourney/generations", r.URL.Path)
		require.Equal(t, "Bearer provider-test", r.Header.Get("Authorization"))
		var body map[string]any
		require.NoError(t, common.DecodeJson(r.Body, &body))
		require.Equal(t, "7", body["version"])
		require.Equal(t, true, body["niji"])
		require.Equal(t, "fast", body["speed"])
		require.Equal(t, float64(4), body["repeat"])
		require.NotContains(t, body, "model")
		require.Contains(t, body["webhook"], "https://apimaster.ai/api/tasks/imagine/")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"data":[{"task_id":"provider-one","status":"submitted"}]}`))
	}))
	defer upstream.Close()
	ratio := 2.0
	recharge := 1.0
	channel := model.Channel{Type: 1, Name: "imagine", Key: "provider-test", BaseURL: &upstream.URL, RechargeRate: &recharge, ApimasterPriceRatio: &ratio}
	require.NoError(t, db.Create(&channel).Error)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("POST", "/v1/midjourney/generations", strings.NewReader(`{"model":"midjourney-niji-7","prompt":"teapot","repeat":4,"speed":"fast"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(ctx, constant.ContextKeyUserId, user.Id)
	common.SetContextKey(ctx, constant.ContextKeyTokenId, token.Id)
	common.SetContextKey(ctx, constant.ContextKeyTokenKey, token.Key)
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyUserSetting, dto.UserSetting{BillingPreference: "wallet_only"})
	common.SetContextKey(ctx, constant.ContextKeyChannelId, channel.Id)
	common.SetContextKey(ctx, constant.ContextKeyChannelType, 1)
	common.SetContextKey(ctx, constant.ContextKeyChannelBaseUrl, upstream.URL)
	common.SetContextKey(ctx, constant.ContextKeyChannelKey, "provider-test")
	RelayImagine(ctx)
	require.Equal(t, 200, recorder.Code, recorder.Body.String())
	require.NotContains(t, recorder.Body.String(), "provider-one")
	require.NotContains(t, recorder.Body.String(), "provider-test")
	var batch model.ImagineBatch
	require.NoError(t, db.First(&batch).Error)
	require.Equal(t, 1, batch.ActualTaskCount)
	require.InDelta(t, 0.05504, batch.BaseUnitPrice, 1e-9)
	multiplier := 2 * ratio_setting.GetGroupRatio("default")
	require.InDelta(t, multiplier, batch.FinalMultiplier, 1e-9)
	require.NoError(t, db.First(&user, user.Id).Error)
	require.Equal(t, 1000000-int(math.Round(0.05504*multiplier*common.QuotaPerUnit)), user.Quota)
}
