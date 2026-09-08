package service

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupChannelModelStatusDB(t *testing.T) (*gorm.DB, *model.Channel) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.ChannelModelEvent{}))
	original, memoryCache := model.DB, common.MemoryCacheEnabled
	model.DB, common.MemoryCacheEnabled = db, false
	t.Cleanup(func() { model.DB, common.MemoryCacheEnabled = original, memoryCache })
	channel := &model.Channel{Id: 214, Name: "test", Status: 1, Models: "gpt-5.6-sol,gpt-5.6-terra", Group: "default,vip", Key: "test"}
	require.NoError(t, channel.Insert())
	return db, channel
}

func TestModelDisableAndRecoveryRetainAuditHistory(t *testing.T) {
	db, channel := setupChannelModelStatusDB(t)
	changed, err := setAutomaticModelStatus(channel.Id, "gpt-5.6-sol", "HTTP 502 probe failed", false, "health_probe")
	require.NoError(t, err)
	require.True(t, changed)
	changed, err = setAutomaticModelStatus(channel.Id, "gpt-5.6-sol", "duplicate", false, "health_probe")
	require.NoError(t, err)
	require.False(t, changed)
	current, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	require.Contains(t, current.GetOtherInfo()["auto_disabled_models"], "gpt-5.6-sol")
	var enabled int64
	require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ? AND model = ? AND enabled = ?", channel.Id, "gpt-5.6-sol", true).Count(&enabled).Error)
	require.Zero(t, enabled)
	// Editing from a snapshot taken before the disable must not erase it.
	channel.Name = "renamed"
	require.NoError(t, channel.Update())
	require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ? AND model = ? AND enabled = ?", channel.Id, "gpt-5.6-sol", true).Count(&enabled).Error)
	require.Zero(t, enabled)
	changed, err = setAutomaticModelStatus(channel.Id, "gpt-5.6-sol", "Recovery probe passed", true, "recovery_probe")
	require.NoError(t, err)
	require.True(t, changed)
	var events []model.ChannelModelEvent
	require.NoError(t, db.Order("id ASC").Find(&events).Error)
	require.Len(t, events, 2)
	require.Equal(t, "HTTP 502 probe failed", events[0].Reason)
	require.Equal(t, "enable", events[1].Action)
}

func TestModelDisableRollsBackWhenAuditCannotBeWritten(t *testing.T) {
	db, channel := setupChannelModelStatusDB(t)
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("reject_audit", func(tx *gorm.DB) {
		if tx.Statement.Table == "channel_model_events" {
			tx.AddError(errors.New("audit unavailable"))
		}
	}))
	changed, err := setAutomaticModelStatus(channel.Id, "gpt-5.6-sol", "HTTP 502", false, "health_probe")
	require.Error(t, err)
	require.False(t, changed)
	current, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	require.Empty(t, current.GetOtherInfo())
	var disabled int64
	require.NoError(t, db.Model(&model.Ability{}).Where("enabled = ?", false).Count(&disabled).Error)
	require.Zero(t, disabled)
}

func TestManualDisableWinsOverStaleFingerprintRecovery(t *testing.T) {
	db, channel := setupChannelModelStatusDB(t)
	_, err := setAutomaticModelStatus(channel.Id, "gpt-5.6-sol", "HTTP 502", false, "health_probe")
	require.NoError(t, err)
	stale, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	_, err = model.SetChannelModelsManuallyDisabled(channel.Id, []string{"gpt-5.6-sol"}, true, 42)
	require.NoError(t, err)
	recoverModelForFingerprint(stale, "gpt-5.6-sol", map[string]interface{}{})
	changed, err := setAutomaticModelStatus(channel.Id, "gpt-5.6-sol", "Recovery probe passed", true, "recovery_probe")
	require.NoError(t, err)
	require.False(t, changed)
	var enabled int64
	require.NoError(t, db.Model(&model.Ability{}).Where("model = ? AND enabled = ?", "gpt-5.6-sol", true).Count(&enabled).Error)
	require.Zero(t, enabled)
	var event model.ChannelModelEvent
	require.NoError(t, db.Order("id DESC").First(&event).Error)
	require.Equal(t, 42, event.ActorID)
	require.Equal(t, "manual", event.Source)
}

func TestFingerprintRecoveryPreservesOtherModelDisable(t *testing.T) {
	db, channel := setupChannelModelStatusDB(t)
	_, err := setAutomaticModelStatus(channel.Id, "gpt-5.6-sol", "sol failure", false, "health_probe")
	require.NoError(t, err)
	stale, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	_, err = setAutomaticModelStatus(channel.Id, "gpt-5.6-terra", "terra failure", false, "health_probe")
	require.NoError(t, err)
	for i := 0; i < fingerprintRecoveryThreshold; i++ {
		recoverModelForFingerprint(stale, "gpt-5.6-sol", map[string]interface{}{})
	}
	current, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	entries := autoDisabledModelInfo(current.GetOtherInfo())
	require.Contains(t, entries, "gpt-5.6-terra")
	require.NotContains(t, entries, "gpt-5.6-sol")
	var event model.ChannelModelEvent
	require.NoError(t, db.Order("id DESC").First(&event).Error)
	require.Equal(t, "fingerprint_recovery", event.Source)
}

func TestModelDisableRollsBackWhenMetadataCannotBeWritten(t *testing.T) {
	db, channel := setupChannelModelStatusDB(t)
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("reject_metadata", func(tx *gorm.DB) {
		if tx.Statement.Table == "channels" {
			tx.AddError(errors.New("metadata unavailable"))
		}
	}))
	changed, err := setAutomaticModelStatus(channel.Id, "gpt-5.6-sol", "HTTP 502", false, "health_probe")
	require.Error(t, err)
	require.False(t, changed)
	var count int64
	require.NoError(t, db.Model(&model.ChannelModelEvent{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Model(&model.Ability{}).Where("enabled = ?", false).Count(&count).Error)
	require.Zero(t, count)
}

func TestOldRecoveryCannotEnableNewDisableGeneration(t *testing.T) {
	db, channel := setupChannelModelStatusDB(t)
	_, err := setAutomaticModelStatus(channel.Id, "gpt-5.6-sol", "first failure", false, "health_probe")
	require.NoError(t, err)
	stale, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	_, err = setAutomaticModelStatus(channel.Id, "gpt-5.6-sol", "recovered", true, "recovery_probe")
	require.NoError(t, err)
	_, err = setAutomaticModelStatus(channel.Id, "gpt-5.6-sol", "new failure", false, "health_probe")
	require.NoError(t, err)
	changed, err := setAutomaticModelStatus(channel.Id, "gpt-5.6-sol", "stale recovery", true, "recovery_probe", stale.AutoDisabledModelVersion("gpt-5.6-sol"))
	require.NoError(t, err)
	require.False(t, changed)
	recoverModelForFingerprint(stale, "gpt-5.6-sol", map[string]interface{}{})
	current, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	entry := autoDisabledModelInfo(current.GetOtherInfo())["gpt-5.6-sol"].(map[string]interface{})
	require.Zero(t, autoDisabledModelPassCount(entry))
	var count int64
	require.NoError(t, db.Model(&model.ChannelModelEvent{}).Count(&count).Error)
	require.EqualValues(t, 3, count)
}

func TestAbilityRepairPreservesHistoricalMissingReason(t *testing.T) {
	db, channel := setupChannelModelStatusDB(t)
	require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ? AND model = ?", channel.Id, "gpt-5.6-sol").Update("enabled", false).Error)
	_, failures, err := model.FixAbility()
	require.NoError(t, err)
	require.Zero(t, failures)
	var enabled int64
	require.NoError(t, db.Model(&model.Ability{}).Where("model = ? AND enabled = ?", "gpt-5.6-sol", true).Count(&enabled).Error)
	require.Zero(t, enabled)
	current, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	require.Empty(t, current.GetOtherInfo())
}
