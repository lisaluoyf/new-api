package service

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

func TestChannelHealthSeparatesModelEndpointAndStream(t *testing.T) {
	oldEnabled := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = oldEnabled; ClearChannelHealth(163) })
	channel := types.ChannelError{ChannelId: 163, AutoBan: true}
	failing := types.ChannelProbeTarget{ModelName: "gpt-5.6-terra", EndpointType: constant.EndpointTypeOpenAIResponse, IsStream: true}
	healthy := []types.ChannelProbeTarget{
		{ModelName: "gpt-5.6-sol", EndpointType: constant.EndpointTypeOpenAIResponse, IsStream: true},
		{ModelName: failing.ModelName, EndpointType: constant.EndpointTypeOpenAI, IsStream: true},
		{ModelName: failing.ModelName, EndpointType: constant.EndpointTypeOpenAIResponse, IsStream: false},
	}
	err := types.NewOpenAIError(errors.New("upstream failed"), types.ErrorCodeBadResponse, 502)
	for i := 0; i < 4; i++ {
		action, _ := EvaluateChannelHealth(channel, err, failing)
		require.Equal(t, HealthSkip, action)
	}
	for _, target := range healthy {
		for i := 0; i < 50; i++ {
			RecordChannelSuccess(channel.ChannelId, target)
		}
		ClearChannelHealth(channel.ChannelId, target)
		action, _ := EvaluateChannelHealth(channel, err, target)
		require.Equal(t, HealthSkip, action)
	}
	action, _ := EvaluateChannelHealth(channel, err, failing)
	require.Equal(t, HealthProbeBeforeDisable, action)
	ClearChannelHealth(channel.ChannelId, failing)
	action, _ = EvaluateChannelHealth(channel, err, failing)
	require.Equal(t, HealthSkip, action)
}

func TestProbeTargetsPersistAndPreventUnrelatedRecovery(t *testing.T) {
	db, channel := setupChannelModelStatusDB(t)
	name := "gpt-5.6-terra"
	target := types.ChannelProbeTarget{ModelName: name, EndpointType: constant.EndpointTypeOpenAIResponse, IsStream: true}
	changed, err := setAutomaticModelStatusWithTargets(channel.Id, name, "stream failure", false, "health_probe", []types.ChannelProbeTarget{target})
	require.NoError(t, err)
	require.True(t, changed)
	snapshot, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	targets, err := snapshot.AutoDisabledModelProbeTargets(name)
	require.NoError(t, err)
	require.Equal(t, []types.ChannelProbeTarget{target}, targets)
	for i := 0; i < 4; i++ {
		recoverModelForFingerprint(snapshot, name, nil)
	}
	current, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	require.Contains(t, current.GetDisabledModels(), name)
	second := target
	second.IsStream = false
	changed, err = setAutomaticModelStatusWithTargets(channel.Id, name, "second protocol failure", false, "health_probe", []types.ChannelProbeTarget{second})
	require.NoError(t, err)
	require.True(t, changed)
	current, err = model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	targets, err = current.AutoDisabledModelProbeTargets(name)
	require.NoError(t, err)
	require.ElementsMatch(t, []types.ChannelProbeTarget{target, second}, targets)
	changed, err = setAutomaticModelStatus(channel.Id, name, "stale success", true, "recovery_probe", snapshot.AutoDisabledModelVersion(name))
	require.NoError(t, err)
	require.False(t, changed)
	var count int64
	require.NoError(t, db.Table("abilities").Where("channel_id = ? AND model = ? AND enabled = ?", channel.Id, name, true).Count(&count).Error)
	require.Zero(t, count)
}
