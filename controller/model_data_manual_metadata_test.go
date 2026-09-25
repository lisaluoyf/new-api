package controller

import (
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestManualDisableMetadataUsesLatestTransitionAndModel(t *testing.T) {
	db := setupModelDataToggleTestDB(t)
	events := []model.ChannelModelEvent{
		{ChannelID: 170, Model: "gpt-5.6-luna", Action: "disable", Source: "manual", Reason: "upstream unstable", CreatedAt: 100},
		{ChannelID: 171, Model: "gpt-5.6-luna", Action: "disable", Source: "manual", CreatedAt: 101},
		{ChannelID: 171, Model: "gpt-5.6-luna", Action: "enable", Source: "manual", CreatedAt: 102},
		{ChannelID: 172, Model: "gpt-5.6-luna", Action: "disable", Source: "health_probe", CreatedAt: 103},
		{ChannelID: 170, Model: "another-model", Action: "disable", Source: "manual", CreatedAt: 104},
	}
	require.NoError(t, db.Create(&events).Error)
	got := modelDataManualDisableEvents([]int{170, 171, 172, 173}, []string{"gpt-5.6-luna"})
	require.Len(t, got, 1)
	require.Equal(t, "upstream unstable", got[170].Reason)
	require.Equal(t, int64(100), got[170].CreatedAt)
	require.Empty(t, modelDataManualDisableEvents(nil, []string{"gpt-5.6-luna"}))
	require.Empty(t, modelDataManualDisableEvents([]int{170}, nil))
}

func TestManualDisableActorNamesPreferDisplayNameAndHandleMissingUsers(t *testing.T) {
	db := setupModelDataToggleTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}))
	require.NoError(t, db.Create(&model.User{Id: 14, Username: "opaque-user", AffCode: "actor14", DisplayName: "lisa.luoyf"}).Error)
	require.NoError(t, db.Create(&model.User{Id: 19, Username: "operator", AffCode: "actor19", DisplayName: " "}).Error)
	events := map[int]model.ChannelModelEvent{170: {ActorID: 14}, 171: {ActorID: 19}, 172: {ActorID: 999}, 173: {ActorID: 0}}
	got := modelDataManualActorNames(events)
	require.Equal(t, "lisa.luoyf", got[14])
	require.Equal(t, "operator", got[19])
	require.NotContains(t, got, 999)
	require.NotContains(t, got, 0)
	require.Empty(t, modelDataManualActorNames(nil))
}
