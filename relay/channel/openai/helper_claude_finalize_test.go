package openai

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestHandleFinalResponseSynthesizesClaudeStopAfterEOF(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	status := relaycommon.NewStreamStatus()
	status.SetEndReason(relaycommon.StreamEndReasonEOF, nil)
	info := &relaycommon.RelayInfo{
		RelayFormat:       types.RelayFormatClaude,
		ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{},
		StreamStatus:      status,
	}
	lastChunk := `{"id":"chatcmpl-test","choices":[{"delta":{"content":"partial"},"finish_reason":null,"index":0}]}`

	HandleFinalResponse(c, info, lastChunk, "chatcmpl-test", 1, "test-model", "", &dto.Usage{
		PromptTokens:     10,
		CompletionTokens: 4,
	}, false)

	body := recorder.Body.String()
	assert.Equal(t, 1, strings.Count(body, "event: message_start"))
	assert.Equal(t, 1, strings.Count(body, "event: message_stop"))
	assert.Contains(t, body, `"stop_reason":"end_turn"`)
	assert.True(t, info.ClaudeConvertInfo.Done)
	assert.True(t, status.HasErrors())
	assert.Contains(t, status.Errors[0].Message, "EOF without finish_reason")
}

func TestHandleFinalResponseDoesNotDuplicateClaudeStop(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	status := relaycommon.NewStreamStatus()
	status.SetEndReason(relaycommon.StreamEndReasonEOF, nil)
	info := &relaycommon.RelayInfo{
		RelayFormat:       types.RelayFormatClaude,
		SendResponseCount: 1,
		ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{
			LastMessagesType: relaycommon.LastMessageTypeText,
		},
		StreamStatus: status,
	}
	lastChunk := `{"id":"chatcmpl-test","choices":[{"delta":{},"finish_reason":"stop","index":0}]}`

	HandleFinalResponse(c, info, lastChunk, "chatcmpl-test", 1, "test-model", "", &dto.Usage{
		PromptTokens:     10,
		CompletionTokens: 4,
	}, false)

	assert.Equal(t, 1, strings.Count(recorder.Body.String(), "event: message_stop"))
	assert.True(t, info.ClaudeConvertInfo.Done)
	assert.False(t, status.HasErrors())
}
