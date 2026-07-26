package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestInitChannelCacheRespectsAbilityEnabled(t *testing.T) {
	originalDB := DB
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&Channel{}, &Ability{}); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}
	DB = db
	common.MemoryCacheEnabled = true
	t.Cleanup(func() {
		DB = originalDB
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
	})

	channel := Channel{
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
	abilities := []Ability{
		{Group: "default", Model: "claude-opus-5", ChannelId: channel.Id, Enabled: false},
		{Group: "default", Model: "claude-sonnet-5", ChannelId: channel.Id, Enabled: true},
	}
	if err := db.Create(&abilities).Error; err != nil {
		t.Fatalf("create abilities: %v", err)
	}

	InitChannelCache()

	disabled, err := GetRandomSatisfiedChannel("default", "claude-opus-5", 0, nil)
	if err != nil {
		t.Fatalf("select disabled model: %v", err)
	}
	if disabled != nil {
		t.Fatalf("disabled model routed to channel %d", disabled.Id)
	}

	enabled, err := GetRandomSatisfiedChannel("default", "claude-sonnet-5", 0, nil)
	if err != nil {
		t.Fatalf("select enabled model: %v", err)
	}
	if enabled == nil || enabled.Id != channel.Id {
		t.Fatalf("enabled model channel = %#v, want channel %d", enabled, channel.Id)
	}
}
