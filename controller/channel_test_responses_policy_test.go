package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/stretchr/testify/require"
)

func TestChannelProbeHonorsResponsesPolicy(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	saved := settings.ChatCompletionsToResponsesPolicy
	t.Cleanup(func() { settings.ChatCompletionsToResponsesPolicy = saved })
	settings.ChatCompletionsToResponsesPolicy = model_setting.ChatCompletionsToResponsesPolicy{
		Enabled: true, ChannelIDs: []int{79}, ModelPatterns: []string{`^grok-4[.][567]$`},
	}
	channel := &model.Channel{Id: 79, Type: constant.ChannelTypeOpenAI}
	for _, modelName := range []string{"grok-4.5", "grok-4.6", "grok-4.7"} {
		for _, endpoint := range []string{"", "openai", " openai "} {
			t.Run(modelName+"/"+endpoint, func(t *testing.T) {
				require.Equal(t, "openai-response", normalizeChannelTestEndpoint(channel, modelName, endpoint))
			})
		}
	}
	// Explicit non-chat protocols and channels outside the policy retain their probes.
	for _, endpoint := range []string{"anthropic", "gemini", "openai-response", "openai-response-compact"} {
		require.Equal(t, endpoint, normalizeChannelTestEndpoint(channel, "grok-4.5", endpoint))
	}
	require.Equal(t, "openai", normalizeChannelTestEndpoint(channel, "gpt-6-luna", "openai"))
	require.Equal(t, "openai", normalizeChannelTestEndpoint(&model.Channel{Id: 80, Type: constant.ChannelTypeOpenAI}, "grok-4.5", "openai"))
	require.Equal(t, "openai", normalizeChannelTestEndpoint(nil, "grok-4.5", "openai"))
	require.Equal(t, string(constant.EndpointTypeTypeSafe), normalizeChannelTestEndpoint(&model.Channel{Id: 79, Type: constant.ChannelTypeTypeSafe}, "grok-4.5", "openai"))
	settings.ChatCompletionsToResponsesPolicy.Enabled = false
	require.Equal(t, "openai", normalizeChannelTestEndpoint(channel, "grok-4.5", "openai"))
	require.Empty(t, normalizeChannelTestEndpoint(channel, "grok-4.5", ""))
}
