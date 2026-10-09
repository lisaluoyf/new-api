package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestProviderSettingsCatalogAndNewRuleRoundTrip(t *testing.T) {
	oldDB, oldLogDB := model.DB, model.LOG_DB
	t.Cleanup(func() { model.DB, model.LOG_DB = oldDB, oldLogDB })
	initModelListColumnNames(t)
	setupUserAffRatioOverrideControllerTestDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.ChannelModelPricing{}))
	user := model.User{Id: 1, Username: "provider-catalog-owner", Group: "default"}
	require.NoError(t, model.DB.Create(&user).Error)
	baseURL, priceRatio := "https://private-provider.invalid", 1.0
	channel := model.Channel{Id: 38, Name: "private-provider-name", Key: "private-provider-key", BaseURL: &baseURL, Models: "provider-catalog-model", Group: "default", Status: 1, ApimasterPriceRatio: &priceRatio}
	require.NoError(t, model.DB.Create(&channel).Error)
	require.NoError(t, channel.AddAbilities(nil))
	require.NoError(t, model.DB.Create(&model.ChannelModelPricing{ChannelId: 38, ModelName: channel.Models, InputPrice: 2, OutputPrice: 4, GroupRatio: 1, PricingSource: "api"}).Error)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("id", 1)
	GetProviderSettings(ctx)
	require.Contains(t, recorder.Body.String(), `"rules":[]`)
	require.Contains(t, recorder.Body.String(), "provider-catalog-model")
	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	ctx.Set("id", 1)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/user/provider-settings/channels?model=provider-catalog-model", nil)
	GetProviderSettingChannels(ctx)
	require.Contains(t, recorder.Body.String(), `"channel_id":38`)
	require.Contains(t, recorder.Body.String(), `"user_price":2`)
	for _, secret := range []string{channel.Key, channel.Name, baseURL, "input_price", "recharge_rate"} {
		require.NotContains(t, recorder.Body.String(), `"`+secret+`"`)
	}
	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	ctx.Set("id", 1)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/user/provider-settings", strings.NewReader(`{"rules":[{"enabled":true,"model":"provider-catalog-model","mode":"include","channel_ids":[38,38]}]}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	SaveProviderSettings(ctx)
	require.Contains(t, recorder.Body.String(), `"success":true`)
	require.Contains(t, recorder.Body.String(), `"channel_ids":[38]`)
	saved, err := model.GetUserById(1, false)
	require.NoError(t, err)
	require.Equal(t, []int{38}, saved.GetSetting().ProviderSettings[0].ChannelIDs)
}

func TestProviderSettingsSavePreservesOtherUserSettings(t *testing.T) {
	setupUserAffRatioOverrideControllerTestDB(t)
	user := model.User{Id: 1, Username: "provider-owner", Group: "default", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	user.SetSetting(dto.UserSetting{Language: "zh", ModelDiscountRatios: map[string]float64{"test-model": .8}, ProviderSettings: []dto.ProviderSetting{{Model: "test-model", Mode: "include", ChannelIDs: []int{39}}}})
	require.NoError(t, model.DB.Create(&user).Error)
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.ChannelModelPricing{}))
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/user/provider-settings", strings.NewReader(`{"rules":[{"enabled":true,"model":"test-model","mode":"include","channel_ids":[39]}]}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", 1)
	SaveProviderSettings(ctx)
	require.Contains(t, recorder.Body.String(), `"success":true`)
	require.NoError(t, model.DB.First(&user, 1).Error)
	require.True(t, user.GetSetting().ProviderSettings[0].Enabled)
	require.Equal(t, "zh", user.GetSetting().Language)
	require.Equal(t, .8, user.GetSetting().ModelDiscountRatios["test-model"])
	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/user/setting", strings.NewReader(`{"notify_type":"email","quota_warning_threshold":10}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", 1)
	UpdateUserSetting(ctx)
	require.Contains(t, recorder.Body.String(), `"success":true`)
	require.NoError(t, model.DB.First(&user, 1).Error)
	require.Len(t, user.GetSetting().ProviderSettings, 1)
}

func TestProviderSettingsSupportUsableMarketplaceHiddenModels(t *testing.T) {
	initModelListColumnNames(t)
	setupUserAffRatioOverrideControllerTestDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.ChannelModelPricing{}))
	user := model.User{Id: 1, Username: "provider-hidden-model-owner", Group: "default"}
	require.NoError(t, model.DB.Create(&user).Error)
	names := []string{"gpt-5.4", "gpt-5.4-mini", "kimi-k2.5", "sora-2", service.FreeModelID}
	channel := model.Channel{Id: 38, Name: "private-provider", Key: "private-key", Models: strings.Join(names, ","), Group: "default", Status: common.ChannelStatusEnabled}
	require.NoError(t, model.DB.Create(&channel).Error)
	require.NoError(t, channel.AddAbilities(nil))
	models := providerSettingModels(&user)
	require.NotContains(t, models, service.FreeModelID)
	for _, name := range names[:len(names)-1] {
		t.Run(name, func(t *testing.T) {
			require.True(t, isHiddenMarketplaceModel(name))
			require.Contains(t, models, name)
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Set("id", user.Id)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/user/provider-settings/channels?model="+name, nil)
			GetProviderSettingChannels(ctx)
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Contains(t, recorder.Body.String(), `"channel_id":38`)
			require.NotContains(t, recorder.Body.String(), channel.Name)
			require.NotContains(t, recorder.Body.String(), channel.Key)
			recorder = httptest.NewRecorder()
			ctx, _ = gin.CreateTestContext(recorder)
			ctx.Set("id", user.Id)
			body, err := common.Marshal(providerSettingsRequest{Rules: []dto.ProviderSetting{{Model: name, Mode: "include", ChannelIDs: []int{38}, Enabled: true}}})
			require.NoError(t, err)
			ctx.Request = httptest.NewRequest(http.MethodPut, "/api/user/provider-settings", strings.NewReader(string(body)))
			ctx.Request.Header.Set("Content-Type", "application/json")
			SaveProviderSettings(ctx)
			require.Contains(t, recorder.Body.String(), `"success":true`)
			saved, err := model.GetUserById(user.Id, false)
			require.NoError(t, err)
			require.Equal(t, name, saved.GetSetting().ProviderSettings[0].Model)
		})
	}
}

func TestProviderSettingsRejectsUnknownChannelsAndCannotEditOtherUsers(t *testing.T) {
	setupUserAffRatioOverrideControllerTestDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.ChannelModelPricing{}))
	for _, userID := range []int{1, 2} {
		user := model.User{Id: userID, Username: "provider-user", Group: "default", AffCode: "provider-one"}
		if userID == 2 {
			user.Username = "other-provider-user"
			user.AffCode = "provider-two"
		}
		user.SetSetting(dto.UserSetting{ProviderSettings: []dto.ProviderSetting{{Model: "test-model", Mode: "exclude", ChannelIDs: []int{39}}}})
		require.NoError(t, model.DB.Create(&user).Error)
	}
	for _, body := range []string{
		`{"rules":[{"enabled":true,"model":"test-model","mode":"include","channel_ids":[123456]}]}`,
		`{"rules":[{"enabled":true,"model":"test-model","mode":"include","channel_ids":[]}]}`,
		`{}`,
	} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPut, "/api/user/provider-settings", strings.NewReader(body))
		ctx.Request.Header.Set("Content-Type", "application/json")
		ctx.Set("id", 1)
		SaveProviderSettings(ctx)
		require.Equal(t, 400, recorder.Code)
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/user/provider-settings", strings.NewReader(`{"id":2,"rules":[]}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", 1)
	SaveProviderSettings(ctx)
	require.Contains(t, recorder.Body.String(), `"success":true`)
	owner, err := model.GetUserById(1, false)
	require.NoError(t, err)
	require.Empty(t, owner.GetSetting().ProviderSettings)
	other, err := model.GetUserById(2, false)
	require.NoError(t, err)
	require.Len(t, other.GetSetting().ProviderSettings, 1)
}

func TestProviderSettingsConstrainOfficialFallback(t *testing.T) {
	settings := model_setting.GetModelFallbackSettings()
	old := *settings
	*settings = model_setting.ModelFallbackSettings{Policies: []model_setting.OfficialFallbackPolicy{{Enabled: true, ModelID: "test-model", OfficialChannelID: 38, FallbackAfter: 0}}}
	t.Cleanup(func() { *settings = old })
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("original_model", "test-model")
	info := &relaycommon.RelayInfo{OriginModelName: "test-model"}
	retry := 1
	param := &service.RetryParam{Ctx: ctx, ModelName: "test-model", Retry: &retry}
	for _, test := range []struct {
		mode    string
		ids     []int
		allowed bool
	}{
		{"include", []int{1, 38}, true},
		{"include", []int{1, 2}, false},
		{"exclude", []int{38, 39}, false},
		{"exclude", []int{1, 2}, true},
	} {
		common.SetContextKey(ctx, constant.ContextKeyUserSetting, dto.UserSetting{ProviderSettings: []dto.ProviderSetting{{Enabled: true, Model: "test-model", Mode: test.mode, ChannelIDs: test.ids}}})
		_, allowed := shouldUseOfficialFallback(ctx, info, param)
		require.Equal(t, test.allowed, allowed)
		require.Equal(t, test.allowed, shouldRetryForOfficialFallback(ctx, 0))
		require.Equal(t, test.allowed, shouldRetryForOfficialFallbackModel(ctx, 0))
	}
}
