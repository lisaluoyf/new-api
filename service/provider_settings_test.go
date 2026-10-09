package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestNormalizeProviderSettings(t *testing.T) {
	rules, err := NormalizeProviderSettings([]dto.ProviderSetting{{Model: " test-model ", Enabled: true, Mode: "exclude", ChannelIDs: []int{9, 2, 9}}})
	require.NoError(t, err)
	require.Equal(t, "test-model", rules[0].Model)
	require.Equal(t, []int{2, 9}, rules[0].ChannelIDs)
	for _, rules := range [][]dto.ProviderSetting{
		{{Model: "", Mode: "include", ChannelIDs: []int{1}}},
		{{Model: FreeModelID, Mode: "include", ChannelIDs: []int{1}}},
		{{Model: "test-model", Mode: "prefer", ChannelIDs: []int{1}}},
		{{Model: "test-model", Mode: "include"}},
		{{Model: "test-model", Mode: "include", ChannelIDs: []int{-1}}},
		{{Model: "test-model", Mode: "include", ChannelIDs: []int{1}}, {Model: "test-model", Mode: "exclude", ChannelIDs: []int{2}}},
	} {
		_, err := NormalizeProviderSettings(rules)
		require.Error(t, err)
	}
}

func TestProviderSettingsFilter(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.Nil(t, ProviderChannelPickFilter(ctx, "test-model"))
	for _, mode := range []string{"include", "exclude"} {
		common.SetContextKey(ctx, constant.ContextKeyUserSetting, dto.UserSetting{ProviderSettings: []dto.ProviderSetting{{Enabled: true, Model: "test-model", Mode: mode, ChannelIDs: []int{2, 3}}}})
		filter := ChannelPickFilter(ctx, "test-model")
		require.NotNil(t, filter)
		require.False(t, filter(nil))
		require.Equal(t, mode == "include", filter(&model.Channel{Id: 2}))
		require.Equal(t, mode == "exclude", filter(&model.Channel{Id: 1}))
		require.Nil(t, ProviderChannelPickFilter(ctx, "other-model"))
		ctx.Set("provider_settings_request_model", "test-model")
		require.NotNil(t, ProviderChannelPickFilter(ctx, "normalized-model"))
		ctx.Set("provider_settings_request_model", "")
	}
	common.SetContextKey(ctx, constant.ContextKeyUserSetting, dto.UserSetting{ProviderSettings: []dto.ProviderSetting{{Enabled: false, Model: "test-model", Mode: "include", ChannelIDs: []int{2}}}})
	require.Nil(t, ProviderChannelPickFilter(ctx, "test-model"))
}

func TestProviderSettingsComposeClientRestrictions(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	common.SetContextKey(ctx, constant.ContextKeyUserSetting, dto.UserSetting{ProviderSettings: []dto.ProviderSetting{{Enabled: true, Model: "gpt-5.4", Mode: "include", ChannelIDs: []int{1}}}})
	setting := `{"client_exclusive":"codex"}`
	filter := ChannelPickFilter(ctx, "gpt-5.4")
	require.False(t, filter(&model.Channel{Id: 1, Setting: &setting}))
	require.False(t, filter(&model.Channel{Id: 2}))
	require.True(t, filter(&model.Channel{Id: 1}))
}

func TestProviderScopedCheapestRetriesAndCacheIsolation(t *testing.T) {
	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB; InvalidateChannelRoutingCache() })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.ChannelModelPricing{}, &model.Ability{}))
	const name = "provider-scope-test"
	priceRatio := 1.0
	for channelID := 1; channelID <= 40; channelID++ {
		channel := model.Channel{Id: channelID, Name: "route", Key: "test-key", Models: name, Status: 1, Group: AutoCheapestGroup, ApimasterPriceRatio: &priceRatio}
		require.NoError(t, db.Create(&channel).Error)
		require.NoError(t, db.Create(&model.ChannelModelPricing{ChannelId: channelID, ModelName: name, InputPrice: float64(channelID), PricingSource: "api", GroupRatio: 1}).Error)
	}
	require.Equal(t, 1, selectCheapestChannelID(name, nil))
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	policy := dto.ProviderSetting{Enabled: true, Model: name, Mode: "include", ChannelIDs: []int{39, 40}}
	common.SetContextKey(ctx, constant.ContextKeyUserSetting, dto.UserSetting{ProviderSettings: []dto.ProviderSetting{policy}})
	first, err := SelectCheapestEnabledChannel(ctx, name)
	require.NoError(t, err)
	require.Equal(t, 39, first.Id)
	ctx.Set("use_channel", []string{"39"})
	second, err := SelectCheapestEnabledChannel(ctx, name)
	require.NoError(t, err)
	require.Equal(t, 40, second.Id)
	ctx.Set("use_channel", []string{"39", "40"})
	_, err = SelectCheapestEnabledChannel(ctx, name)
	require.ErrorIs(t, err, ErrNoCheapestChannel)
	require.Equal(t, 1, selectCheapestChannelID(name, nil))
	ctx.Set("use_channel", []string{})
	policy.Mode = "exclude"
	policy.ChannelIDs = []int{1, 2}
	common.SetContextKey(ctx, constant.ContextKeyUserSetting, dto.UserSetting{ProviderSettings: []dto.ProviderSetting{policy}})
	first, err = SelectCheapestEnabledChannel(ctx, name)
	require.NoError(t, err)
	require.Equal(t, 3, first.Id)
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 3).Update("status", 2).Error)
	first, err = SelectCheapestEnabledChannel(ctx, name)
	require.NoError(t, err)
	require.Equal(t, 4, first.Id)
	background, err := SelectCheapestEnabledChannelInScope(name, nil, nil, &dto.ProviderSetting{Mode: "include", ChannelIDs: []int{39, 40}})
	require.NoError(t, err)
	require.Equal(t, 39, background.Id)
}
