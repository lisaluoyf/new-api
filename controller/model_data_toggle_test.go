package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestModelDataAliasToggleRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name      string
		models    string
		abilities []model.Ability
	}{
		{"provider casing", "MiniMax-M3", []model.Ability{{Group: "default", Model: "MiniMax-M3", Enabled: true}}},
		{"non-default group", "MiniMax-M3", []model.Ability{{Group: "vip", Model: "MiniMax-M3", Enabled: true}}},
		{"mixed aliases and groups", "minimax-m3,MiniMax-M3", []model.Ability{
			{Group: "default", Model: "minimax-m3", Enabled: false},
			{Group: "default", Model: "MiniMax-M3", Enabled: false},
			{Group: "vip", Model: "MiniMax-M3", Enabled: true},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupModelDataToggleTestDB(t)
			require.NoError(t, db.AutoMigrate(&model.ChannelModelPricing{}, &model.ChannelDetectLog{}))
			channel := model.Channel{Id: 109, Name: "test-minimax", Status: common.ChannelStatusEnabled,
				Key: "test-key", Models: tc.models + ",unrelated-model", Group: "default,vip"}
			require.NoError(t, db.Create(&channel).Error)
			abilities := append(tc.abilities, model.Ability{Group: "default", Model: "unrelated-model", Enabled: true})
			for i := range abilities {
				abilities[i].ChannelId = channel.Id
			}
			require.NoError(t, db.Create(&abilities).Error)
			// Cancel external hub pricing; this test only exercises local status.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			check := func(enabled bool) {
				t.Helper()
				items, _, _, _ := getModelDataItems(ctx, "minimax-m3")
				require.Len(t, items, 1)
				require.Equal(t, enabled, items[0].ModelEnabled)
				require.Equal(t, common.ChannelStatusEnabled, items[0].Status)
				if !enabled {
					require.Equal(t, "manual", items[0].StatusSource)
				}
			}
			check(true)
			for _, action := range []string{"disable", "enable", "disable"} {
				recorder := toggleModelForTest(t, channel.Id, "minimax-m3", action)
				require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
				check(action == "enable")
			}
			var unrelated model.Ability
			require.NoError(t, db.First(&unrelated, "channel_id = ? AND model = ?", channel.Id, "unrelated-model").Error)
			require.True(t, unrelated.Enabled)
		})
	}
}

func setupModelDataToggleTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	originalDB := model.DB
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.ChannelModelEvent{}); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}
	model.DB = db
	common.MemoryCacheEnabled = false
	t.Cleanup(func() {
		model.DB = originalDB
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
	})
	return db
}

func toggleModelForTest(t *testing.T, channelID int, modelName string, action string) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/admin/channel-data/toggle",
		strings.NewReader(`{"channel_id":`+strconv.Itoa(channelID)+`,"model":"`+modelName+`","action":"`+action+`"}`),
	)
	ToggleChannelStatus(context)
	return recorder
}

func TestToggleChannelStatusDisablesOnlyRequestedModel(t *testing.T) {
	db := setupModelDataToggleTestDB(t)
	channel := model.Channel{
		Id:     112,
		Name:   "test-channel",
		Status: common.ChannelStatusEnabled,
		Key:    "test-key",
		Models: "claude-opus-5,claude-sonnet-5",
		Group:  "default",
	}
	channel.SetOtherInfo(map[string]interface{}{
		"auto_disabled_models": map[string]interface{}{
			"claude-opus-5": map[string]interface{}{"pass_count": 1},
		},
	})
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	abilities := []model.Ability{
		{Group: "default", Model: "claude-opus-5", ChannelId: channel.Id, Enabled: true},
		{Group: "default", Model: "claude-sonnet-5", ChannelId: channel.Id, Enabled: true},
	}
	if err := db.Create(&abilities).Error; err != nil {
		t.Fatalf("create abilities: %v", err)
	}

	recorder := toggleModelForTest(t, channel.Id, "claude-opus-5", "disable")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var gotChannel model.Channel
	if err := db.First(&gotChannel, channel.Id).Error; err != nil {
		t.Fatalf("load channel: %v", err)
	}
	if gotChannel.Status != common.ChannelStatusEnabled {
		t.Fatalf("channel status = %d, want enabled", gotChannel.Status)
	}

	var disabledAbility model.Ability
	if err := db.First(&disabledAbility, "channel_id = ? AND model = ?", channel.Id, "claude-opus-5").Error; err != nil {
		t.Fatalf("load disabled ability: %v", err)
	}
	if disabledAbility.Enabled {
		t.Fatal("requested model ability is still enabled")
	}

	var untouchedAbility model.Ability
	if err := db.First(&untouchedAbility, "channel_id = ? AND model = ?", channel.Id, "claude-sonnet-5").Error; err != nil {
		t.Fatalf("load untouched ability: %v", err)
	}
	if !untouchedAbility.Enabled {
		t.Fatal("unrelated model ability was disabled")
	}

	if err := model.UpdateAbilityStatus(channel.Id, false); err != nil {
		t.Fatalf("globally disable abilities: %v", err)
	}
	if err := model.UpdateAbilityStatus(channel.Id, true); err != nil {
		t.Fatalf("globally re-enable abilities: %v", err)
	}
	if err := db.First(&disabledAbility, "channel_id = ? AND model = ?", channel.Id, "claude-opus-5").Error; err != nil {
		t.Fatalf("reload disabled ability after global enable: %v", err)
	}
	if disabledAbility.Enabled {
		t.Fatal("requested model was re-enabled by global channel enable")
	}

	var persistedChannel model.Channel
	if err := db.First(&persistedChannel, channel.Id).Error; err != nil {
		t.Fatalf("load persisted channel: %v", err)
	}
	if _, ok := persistedChannel.GetManuallyDisabledModels()["claude-opus-5"]; !ok {
		t.Fatal("manual model disable was not persisted")
	}
	if _, ok := persistedChannel.GetOtherInfo()["auto_disabled_models"]; ok {
		t.Fatal("manual model disable did not clear automatic recovery metadata")
	}

	// Editing a channel rebuilds all abilities; the manual model disable must
	// survive while unrelated models remain routable.
	persistedChannel.Remark = stringPointerForToggleTest("edited")
	if err := persistedChannel.Update(); err != nil {
		t.Fatalf("update channel: %v", err)
	}
	if err := db.First(&disabledAbility, "channel_id = ? AND model = ?", channel.Id, "claude-opus-5").Error; err != nil {
		t.Fatalf("reload disabled ability: %v", err)
	}
	if disabledAbility.Enabled {
		t.Fatal("requested model was re-enabled after channel update")
	}
	if err := db.First(&untouchedAbility, "channel_id = ? AND model = ?", channel.Id, "claude-sonnet-5").Error; err != nil {
		t.Fatalf("reload untouched ability: %v", err)
	}
	if !untouchedAbility.Enabled {
		t.Fatal("unrelated model was disabled after channel update")
	}
}

func TestUpdateChannelPreservesManualModelDisableWhenOtherInfoIsEmpty(t *testing.T) {
	db := setupModelDataToggleTestDB(t)
	channel := model.Channel{
		Id:        116,
		Name:      "test-channel",
		Status:    common.ChannelStatusEnabled,
		Key:       "test-key",
		Models:    "claude-opus-5,claude-sonnet-5",
		Group:     "default",
		OtherInfo: "{}",
	}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	abilities := []model.Ability{
		{Group: "default", Model: "claude-opus-5", ChannelId: channel.Id, Enabled: true},
		{Group: "default", Model: "claude-sonnet-5", ChannelId: channel.Id, Enabled: true},
	}
	if err := db.Create(&abilities).Error; err != nil {
		t.Fatalf("create abilities: %v", err)
	}
	if recorder := toggleModelForTest(t, channel.Id, "claude-opus-5", "disable"); recorder.Code != http.StatusOK {
		t.Fatalf("disable status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPut,
		"/api/channel/",
		strings.NewReader(`{"id":116,"name":"edited-channel","status":1,"key":"test-key","models":"claude-opus-5,claude-sonnet-5","group":"default","other_info":"{}"}`),
	)
	UpdateChannel(context)
	if recorder.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var gotChannel model.Channel
	if err := db.First(&gotChannel, channel.Id).Error; err != nil {
		t.Fatalf("load channel: %v", err)
	}
	if _, ok := gotChannel.GetManuallyDisabledModels()["claude-opus-5"]; !ok {
		t.Fatal("channel edit discarded manual model disable metadata")
	}
	var disabledAbility model.Ability
	if err := db.First(&disabledAbility, "channel_id = ? AND model = ?", channel.Id, "claude-opus-5").Error; err != nil {
		t.Fatalf("load disabled ability: %v", err)
	}
	if disabledAbility.Enabled {
		t.Fatal("channel edit re-enabled manually disabled model")
	}
}

func stringPointerForToggleTest(value string) *string {
	return &value
}

func TestToggleChannelStatusEnableClearsPersistentDisable(t *testing.T) {
	db := setupModelDataToggleTestDB(t)
	channel := model.Channel{
		Id:     114,
		Name:   "test-channel",
		Status: common.ChannelStatusEnabled,
		Key:    "test-key",
		Models: "gpt-5.6-sol",
		Group:  "default,vip",
	}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	abilities := []model.Ability{
		{Group: "default", Model: "gpt-5.6-sol", ChannelId: channel.Id, Enabled: true},
		{Group: "vip", Model: "gpt-5.6-sol", ChannelId: channel.Id, Enabled: true},
	}
	if err := db.Create(&abilities).Error; err != nil {
		t.Fatalf("create abilities: %v", err)
	}

	if recorder := toggleModelForTest(t, channel.Id, "gpt-5.6-sol", "disable"); recorder.Code != http.StatusOK {
		t.Fatalf("disable status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if recorder := toggleModelForTest(t, channel.Id, "gpt-5.6-sol", "enable"); recorder.Code != http.StatusOK {
		t.Fatalf("enable status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var gotChannel model.Channel
	if err := db.First(&gotChannel, channel.Id).Error; err != nil {
		t.Fatalf("load channel: %v", err)
	}
	if len(gotChannel.GetManuallyDisabledModels()) != 0 {
		t.Fatal("manual model disable metadata was not cleared")
	}
	if err := gotChannel.Update(); err != nil {
		t.Fatalf("update channel: %v", err)
	}
	var disabledCount int64
	if err := db.Model(&model.Ability{}).
		Where("channel_id = ? AND model = ? AND enabled = ?", channel.Id, "gpt-5.6-sol", false).
		Count(&disabledCount).Error; err != nil {
		t.Fatalf("count disabled abilities: %v", err)
	}
	if disabledCount != 0 {
		t.Fatalf("disabled ability count = %d, want 0", disabledCount)
	}
}

func TestToggleChannelStatusRejectsMissingAbility(t *testing.T) {
	db := setupModelDataToggleTestDB(t)
	channel := model.Channel{
		Id:     115,
		Name:   "test-channel",
		Status: common.ChannelStatusEnabled,
		Key:    "test-key",
		Models: "gpt-5.6-terra",
		Group:  "default",
	}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}

	recorder := toggleModelForTest(t, channel.Id, "gpt-5.6-sol", "disable")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body = %s", recorder.Code, http.StatusNotFound, recorder.Body.String())
	}
}

func TestToggleChannelStatusDoesNotReenableGloballyDisabledChannel(t *testing.T) {
	db := setupModelDataToggleTestDB(t)
	channel := model.Channel{
		Id:     113,
		Name:   "disabled-channel",
		Status: common.ChannelStatusManuallyDisabled,
		Key:    "test-key",
		Models: "claude-opus-5",
		Group:  "default",
	}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	ability := model.Ability{
		Group: "default", Model: "claude-opus-5", ChannelId: channel.Id, Enabled: false,
	}
	if err := db.Create(&ability).Error; err != nil {
		t.Fatalf("create ability: %v", err)
	}

	recorder := toggleModelForTest(t, channel.Id, "claude-opus-5", "enable")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var gotChannel model.Channel
	if err := db.First(&gotChannel, channel.Id).Error; err != nil {
		t.Fatalf("load channel: %v", err)
	}
	if gotChannel.Status != common.ChannelStatusManuallyDisabled {
		t.Fatalf("channel status = %d, want manually disabled", gotChannel.Status)
	}
}
