package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

func TestNoResponseCancellationHealthThresholdAndIsolation(t *testing.T) {
	old := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = old; ClearChannelHealth(270) })
	ClearChannelHealth(270)
	ch := types.ChannelError{ChannelId: 270, AutoBan: true}
	target := types.ChannelProbeTarget{ModelName: "gpt-6.1-sol", EndpointType: constant.EndpointTypeOpenAI, IsStream: true}
	for i := 0; i < 20; i++ {
		action, _ := EvaluateChannelNoResponseCancellation(ch, target, 119*time.Second, 0)
		require.Equal(t, HealthSkip, action)
		action, _ = EvaluateChannelNoResponseCancellation(ch, target, 180*time.Second, 1)
		require.Equal(t, HealthSkip, action)
	}
	for i := 0; i < 4; i++ {
		action, _ := EvaluateChannelNoResponseCancellation(ch, target, 180*time.Second, 0)
		require.Equal(t, HealthSkip, action)
	}
	other := target
	other.ModelName = "gpt-6-sol"
	action, _ := EvaluateChannelNoResponseCancellation(ch, other, 180*time.Second, 0)
	require.Equal(t, HealthSkip, action)
	other = target
	other.EndpointType = constant.EndpointTypeOpenAIResponse
	action, _ = EvaluateChannelNoResponseCancellation(ch, other, 180*time.Second, 0)
	require.Equal(t, HealthSkip, action)
	action, reason := EvaluateChannelNoResponseCancellation(ch, target, 180*time.Second, 0)
	require.Equal(t, HealthProbeBeforeDisable, action)
	require.Contains(t, reason, "5/5")
	ClearChannelHealth(270, target)
	action, _ = EvaluateChannelNoResponseCancellation(ch, target, 180*time.Second, 0)
	require.Equal(t, HealthSkip, action, "successful independent probe clears the observation window")
}

func TestNoResponseCancellationExclusionsAndSwitches(t *testing.T) {
	old := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = old; ClearChannelHealth(271) })
	ch := types.ChannelError{ChannelId: 271, AutoBan: true}
	target := types.ChannelProbeTarget{ModelName: "gpt-6.1-sol", EndpointType: constant.EndpointTypeOpenAI, IsStream: true}
	for _, endpoint := range []constant.EndpointType{constant.EndpointTypeImageGeneration, constant.EndpointTypeEmbeddings, constant.EndpointTypeJinaRerank} {
		other := target
		other.EndpointType = endpoint
		require.False(t, ChannelNoResponseCancellationEligible(other, 180*time.Second, 0))
	}
	other := target
	other.IsStream = false
	require.False(t, ChannelNoResponseCancellationEligible(other, 180*time.Second, 0))
	for i := 0; i < 10; i++ {
		ch.AutoBan = false
		action, _ := EvaluateChannelNoResponseCancellation(ch, target, 180*time.Second, 0)
		require.Equal(t, HealthSkip, action)
		ch.AutoBan = true
		common.AutomaticDisableChannelEnabled = false
		action, _ = EvaluateChannelNoResponseCancellation(ch, target, 180*time.Second, 0)
		require.Equal(t, HealthSkip, action)
		common.AutomaticDisableChannelEnabled = true
	}
	action, _ := EvaluateChannelNoResponseCancellation(ch, target, 180*time.Second, 0)
	require.Equal(t, HealthSkip, action, "disabled health switches must not accumulate faults")
}

func TestNoUsableOutputCancellationRequiresFailedProbePolicy(t *testing.T) {
	previous := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = previous; ClearChannelHealth(265) })
	ClearChannelHealth(265)
	channel := types.ChannelError{ChannelId: 265, AutoBan: true}
	target := types.ChannelProbeTarget{ModelName: "gpt-6-astra", EndpointType: constant.EndpointTypeOpenAIResponse, IsStream: true}
	for n := 0; n < 10; n++ {
		action, _ := EvaluateChannelNoUsableOutputCancellation(channel, target, 30*time.Second)
		require.Equal(t, HealthSkip, action)
		channel.AutoBan = false
		action, _ = EvaluateChannelNoUsableOutputCancellation(channel, target, 180*time.Second)
		require.Equal(t, HealthSkip, action)
		channel.AutoBan = true
		common.AutomaticDisableChannelEnabled = false
		action, _ = EvaluateChannelNoUsableOutputCancellation(channel, target, 180*time.Second)
		require.Equal(t, HealthSkip, action)
		common.AutomaticDisableChannelEnabled = true
	}
	for n := 0; n < 4; n++ {
		action, _ := EvaluateChannelNoUsableOutputCancellation(channel, target, 180*time.Second)
		require.Equal(t, HealthSkip, action)
	}
	other := target
	other.ModelName = "gpt-6-sol"
	action, _ := EvaluateChannelNoUsableOutputCancellation(channel, other, 180*time.Second)
	require.Equal(t, HealthSkip, action)
	action, _ = EvaluateChannelNoUsableOutputCancellation(channel, target, 180*time.Second)
	require.Equal(t, HealthProbeBeforeDisable, action)
	ClearChannelHealth(265, target)
	action, _ = EvaluateChannelNoUsableOutputCancellation(channel, target, 180*time.Second)
	require.Equal(t, HealthSkip, action)
}
