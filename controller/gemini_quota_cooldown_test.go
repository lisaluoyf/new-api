package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestModelQuotaCooldownIsolationAndRecovery(t *testing.T) {
	initModelListColumnNames(t)
	db := setupModelDataToggleTestDB(t)
	oldRatios := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"gemini-4-argon":1,"gemini-3.8-flash":1}`))
	t.Cleanup(func() { _ = ratio_setting.UpdateModelRatioByJSONString(oldRatios) })
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Log{}, &model.ChannelModelPricing{}))
	oldLogDB, oldRedis := model.LOG_DB, common.RedisEnabled
	model.LOG_DB, common.RedisEnabled = db, false
	t.Cleanup(func() { model.LOG_DB, common.RedisEnabled = oldLogDB, oldRedis })
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "root", Role: common.RoleRootUser, Group: "default", Quota: 1000000}).Error)
	t.Setenv("FEISHU_CHANNEL_CHAT_ID", "")
	t.Setenv("FEISHU_OPS_CHAT_ID", "")
	if service.GetHttpClient() == nil {
		service.InitHttpClient()
	}
	var healthy atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		require.NoError(t, common.DecodeJson(r.Body, &body))
		w.Header().Set("Content-Type", "application/json")
		if body.Model == "gemini-4-argon" && !healthy.Load() {
			w.WriteHeader(503)
			_, _ = io.WriteString(w, `{"error":{"type":"server_error","code":"auth_unavailable","message":"auth_unavailable: no auth available; Individual quota reached. RESOURCE_EXHAUSTED"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"test","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":1,"total_tokens":8}}`)
	}))
	defer upstream.Close()
	base, autoBan := upstream.URL, 1
	channel := &model.Channel{Id: 65, Type: constant.ChannelTypeOpenAI, Name: "quota-test", Status: common.ChannelStatusEnabled, AutoBan: &autoBan, BaseURL: &base, Key: "test-key", Models: "gemini-4-argon,gemini-3.8-flash", Group: "default"}
	require.NoError(t, channel.Insert())
	target := types.ChannelProbeTarget{ModelName: "gemini-4-argon", EndpointType: constant.EndpointTypeOpenAI}
	channelErr := types.ChannelError{ChannelId: 65, ChannelType: channel.Type, ChannelName: channel.Name, AutoBan: true}
	quotaErr := types.WithOpenAIError(types.OpenAIError{Type: "server_error", Code: "model_cooldown", Message: "All credentials for model gemini-4-argon-high are cooling down"}, 429)
	healthy.Store(true)
	probeBeforeDisablingChannel(channelErr, quotaErr, "transient quota cooldown", target.ModelName, target)
	kept, err := model.GetChannelById(65, true)
	require.NoError(t, err)
	require.NotContains(t, kept.GetDisabledModels(), target.ModelName, "a successful independent probe must keep the route enabled")
	healthy.Store(false)
	probeBeforeDisablingChannel(channelErr, quotaErr, "model quota cooldown", target.ModelName, target)
	current, err := model.GetChannelById(65, true)
	require.NoError(t, err)
	require.Contains(t, current.GetDisabledModels(), target.ModelName)
	require.NotContains(t, current.GetDisabledModels(), "gemini-3.8-flash")
	require.Equal(t, common.ChannelStatusEnabled, current.Status)
	saved, err := current.AutoDisabledModelProbeTargets(target.ModelName)
	require.NoError(t, err)
	require.Equal(t, []types.ChannelProbeTarget{target}, saved)
	other, _ := probeChannelForAutomation(current, "gemini-3.8-flash")
	require.NoError(t, other.localErr)
	require.Nil(t, other.newAPIError)
	recovery, _ := probeAutoDisabledModel(current, target.ModelName)
	require.NotNil(t, recovery.newAPIError)
	healthy.Store(true)
	recovery, _ = probeAutoDisabledModel(current, target.ModelName)
	require.NoError(t, recovery.localErr)
	require.Nil(t, recovery.newAPIError)
	service.EnableChannelModel(current.Id, target.ModelName, current.Name, current.AutoDisabledModelVersion(target.ModelName))
	current, err = model.GetChannelById(65, true)
	require.NoError(t, err)
	require.NotContains(t, current.GetDisabledModels(), target.ModelName)
	require.Equal(t, common.ChannelStatusEnabled, current.Status)
}
