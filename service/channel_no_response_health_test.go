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
