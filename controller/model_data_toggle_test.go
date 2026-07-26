package controller

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupModelDataToggleTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	originalDB := model.DB
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.Channel{}, &model.Ability{}); err != nil {
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
