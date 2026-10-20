package service

import (
	"errors"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRecurringFaultsSurviveSuccessfulProbes(t *testing.T) {
	old := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	resetChannelHealthForTest()
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = old; resetChannelHealthForTest() })
	ch := types.ChannelError{ChannelId: 258, AutoBan: true}
	target := types.ChannelProbeTarget{ModelName: "gpt-6-astra", EndpointType: constant.EndpointTypeOpenAIResponse, IsStream: true}
	err := types.NewOpenAIError(errors.New("upstream failed"), types.ErrorCodeBadResponse, 502)
	for episode := 0; episode < 3; episode++ {
		for i := 0; i < 5; i++ {
			action, _ := EvaluateChannelHealth(ch, err, target)
			if i == 4 {
				require.Equal(t, HealthProbeBeforeDisable, action)
			}
		}
		require.Equal(t, episode == 2, RecordPassedDisableProbe(ch, err, target))
		ClearChannelHealth(ch.ChannelId, target)
	}
	// The cooldown starts a new episode series; unrelated targets and gates do not count.
	require.False(t, RecordPassedDisableProbe(ch, err, target))
	other := target
	other.IsStream = false
	require.False(t, RecordPassedDisableProbe(ch, err, other))
	other = target
	other.ModelName = "gpt-6-sol"
	require.False(t, RecordPassedDisableProbe(ch, err, other))
	other = target
	other.EndpointType = constant.EndpointTypeOpenAI
	require.False(t, RecordPassedDisableProbe(ch, err, other))
	ch.AutoBan = false
	require.False(t, RecordPassedDisableProbe(ch, err, target))
	ch.AutoBan = true
	common.AutomaticDisableChannelEnabled = false
	require.False(t, RecordPassedDisableProbe(ch, err, target))
	common.AutomaticDisableChannelEnabled = true
	skipped := types.NewOpenAIError(errors.New("invalid parameter"), types.ErrorCodeBadResponse, 400)
	require.False(t, RecordPassedDisableProbe(ch, skipped, target))
	s := &passedDisableProbes{times: []time.Time{time.Now().Add(-31 * time.Minute), time.Now().Add(-31 * time.Minute)}}
	channelPassedDisableProbes.Store(healthKey(ch.ChannelId, []types.ChannelProbeTarget{target}), s)
	require.False(t, RecordPassedDisableProbe(ch, err, target))
}

func TestCooldownPersistsAndBlocksRecoveryUntilExpiry(t *testing.T) {
	db, channel := setupChannelModelStatusDB(t)
	name := "gpt-5.6-sol"
	target := types.ChannelProbeTarget{ModelName: name, EndpointType: constant.EndpointTypeOpenAIResponse, IsStream: true}
	changed, err := setAutomaticModelStatusWithTargets(channel.Id, name, "recurring faults", false, "health_cooldown", []types.ChannelProbeTarget{target})
	require.NoError(t, err)
	require.True(t, changed)
	current, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	entry := autoDisabledModelInfo(current.GetOtherInfo())[name]
	require.True(t, modelHealthCooldownActive(entry, common.GetTimestamp()))
	second := target
	second.IsStream = false
	changed, err = setAutomaticModelStatusWithTargets(channel.Id, name, "in-flight failure on second target", false, "health_probe", []types.ChannelProbeTarget{second})
	require.NoError(t, err)
	require.True(t, changed)
	current, err = model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	require.True(t, modelHealthCooldownActive(autoDisabledModelInfo(current.GetOtherInfo())[name], common.GetTimestamp()))
	changed, err = setAutomaticModelStatus(channel.Id, name, "probe passed", true, "recovery_probe")
	require.NoError(t, err)
	require.False(t, changed)
	recoverModelForFingerprint(current, name, nil)
	var count int64
	require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ? AND model = ? AND enabled = ?", channel.Id, name, true).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, model.WithChannelOtherInfo(channel.Id, func(tx *gorm.DB, ch *model.Channel) error {
		info := ch.GetOtherInfo()
		autoDisabledModelInfo(info)[name].(map[string]interface{})["cooldown_until"] = common.GetTimestamp() - 1
		ch.SetOtherInfo(info)
		return nil
	}))
	changed, err = setAutomaticModelStatus(channel.Id, name, "probe passed", true, "recovery_probe")
	require.NoError(t, err)
	require.True(t, changed)
	var events []model.ChannelModelEvent
	require.NoError(t, db.Order("id").Find(&events).Error)
	require.Len(t, events, 3)
	require.Equal(t, "health_cooldown", events[0].Source)
}
