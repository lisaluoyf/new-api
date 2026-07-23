package service

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFinalizeOpenAI2ClaudeStream(t *testing.T) {
	info := &relaycommon.RelayInfo{
		SendResponseCount: 1,
		ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{
			LastMessagesType: relaycommon.LastMessageTypeText,
		},
	}
	usage := &dto.Usage{PromptTokens: 10, CompletionTokens: 4}

	responses := FinalizeOpenAI2ClaudeStream(info, usage)

	require.Len(t, responses, 3)
	assert.Equal(t, "content_block_stop", responses[0].Type)
	assert.Equal(t, "message_delta", responses[1].Type)
	require.NotNil(t, responses[1].Delta)
	require.NotNil(t, responses[1].Delta.StopReason)
	assert.Equal(t, "end_turn", *responses[1].Delta.StopReason)
	assert.Equal(t, "message_stop", responses[2].Type)
	assert.True(t, info.ClaudeConvertInfo.Done)
	assert.Empty(t, FinalizeOpenAI2ClaudeStream(info, usage))
}

func TestFinalizeOpenAI2ClaudeStreamPreservesFinishReason(t *testing.T) {
	info := &relaycommon.RelayInfo{
		SendResponseCount: 1,
		ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{
			LastMessagesType: relaycommon.LastMessageTypeText,
			FinishReason:     "length",
		},
	}

	responses := FinalizeOpenAI2ClaudeStream(info, &dto.Usage{})

	require.Len(t, responses, 3)
	require.NotNil(t, responses[1].Delta)
	require.NotNil(t, responses[1].Delta.StopReason)
	assert.Equal(t, "max_tokens", *responses[1].Delta.StopReason)
}
