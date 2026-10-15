package controller

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

func chatRetryTestResponse(frames ...string) *http.Response {
	var body strings.Builder
	for _, frame := range frames {
		body.WriteString("data: " + frame + "\n\n")
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body.String()))}
}

func chatRetryTestInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		OriginModelName: "deepseek-v4-flash", IsStream: true, StartTime: time.Now(),
		RelayFormat: types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "deepseek-v4-flash"},
	}
}

func enableChatRetryTest(t *testing.T) {
	t.Helper()
	previous := operation_setting.IsUpstreamFalseSuccessEnabled()
	oldTimeout := constant.StreamingTimeout
	operation_setting.SetUpstreamFalseSuccessEnabled(true)
	constant.StreamingTimeout = 3
	t.Cleanup(func() {
		operation_setting.SetUpstreamFalseSuccessEnabled(previous)
		constant.StreamingTimeout = oldTimeout
	})
}

func TestChatPreOutputErrorCanFallbackWithoutLeakingPrimaryFrames(t *testing.T) {
	enableChatRetryTest(t)
	for name, frame := range map[string]string{
		"role":               `{"id":"primary","choices":[{"delta":{"role":"assistant"}}]}`,
		"blank_content":      `{"id":"primary","choices":[{"delta":{"content":" \n\t"}}]}`,
		"blank_reasoning":    `{"id":"primary","choices":[{"delta":{"reasoning_content":"\n "}}]}`,
		"empty_tool":         `{"id":"primary","choices":[{"delta":{"tool_calls":[{"index":0}]}}]}`,
		"tool_id_only":       `{"id":"primary","choices":[{"delta":{"tool_calls":[{"index":0,"id":"primary_tool","type":"function","function":{"name":"","arguments":""}}]}}]}`,
		"empty_function":     `{"id":"primary","choices":[{"delta":{"function_call":{}}}]}`,
		"metadata":           `{"id":"primary","choices":[],"usage":{"prompt_tokens":2,"completion_tokens":0,"total_tokens":2}}`,
		"unicode_whitespace": `{"id":"primary","choices":[{"delta":{"content":"\u3000"}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			c, recorder := streamRetryContext()
			require.NoError(t, helper.PingData(c))
			primaryInfo := chatRetryTestInfo()
			_, relayErr := openai.OaiStreamHandler(c, primaryInfo, chatRetryTestResponse(
				frame, `{"error":{"type":"server_error","code":"upstream_failed","message":"private upstream failure"}}`,
			))
			require.NotNil(t, relayErr)
			require.True(t, primaryInfo.StreamOutput.FirstUsableOutput().IsZero())
			require.Equal(t, "heartbeat_only", helper.StreamOutputState(c))
			require.NotContains(t, recorder.Body.String(), "primary")
			decision := evaluateRetry(c, relayErr, 0, 2)
			require.True(t, decision.ShouldRetry)
			setRetryDecision(c, decision)
			detail, ok := getRetryDecision(c)
			require.True(t, ok)
			diagnostics := detail["stream_output_diagnostics"].(helper.StreamOutputDiagnostics)
			require.Equal(t, 2, diagnostics.Frames)
			require.Equal(t, "error", diagnostics.LastFrameKind)
			require.Empty(t, diagnostics.FirstUsableKind)
			require.Equal(t, 1, diagnostics.BufferedFrames)
			_, err := openai.OaiStreamHandler(c, chatRetryTestInfo(), chatRetryTestResponse(
				`{"id":"fallback","choices":[{"index":0,"delta":{"content":"pong"},"finish_reason":"stop"}]}`, "[DONE]",
			))
			require.Nil(t, err)
			require.Contains(t, recorder.Body.String(), "pong")
			require.NotContains(t, recorder.Body.String(), "primary")
			require.NotContains(t, recorder.Body.String(), "private upstream failure")
		})
	}
}

func TestChatRealOutputPreventsFallbackAfterStreamError(t *testing.T) {
	enableChatRetryTest(t)
	for kind, delta := range map[string]string{
		"content":       `{"content":"answer"}`,
		"reasoning":     `{"reasoning_content":"reason"}`,
		"tool_call":     `{"tool_calls":[{"index":0,"function":{"name":"lookup","arguments":""}}]}`,
		"function_call": `{"function_call":{"name":"lookup"}}`,
		"refusal":       `{"refusal":"Cannot comply"}`,
	} {
		t.Run(kind, func(t *testing.T) {
			c, recorder := streamRetryContext()
			info := chatRetryTestInfo()
			_, relayErr := openai.OaiStreamHandler(c, info, chatRetryTestResponse(
				`{"id":"primary","choices":[{"delta":`+delta+`}]}`,
				`{"error":{"type":"server_error","message":"failed later"}}`,
			))
			require.NotNil(t, relayErr)
			require.False(t, info.StreamOutput.FirstUsableOutput().IsZero())
			require.Contains(t, recorder.Body.String(), "primary")
			decision := evaluateRetry(c, relayErr, 0, 2)
			require.False(t, decision.ShouldRetry)
			require.Equal(t, "stream_already_started", decision.Reason)
			setRetryDecision(c, decision)
			detail, ok := getRetryDecision(c)
			require.True(t, ok)
			diagnostics := detail["stream_output_diagnostics"].(helper.StreamOutputDiagnostics)
			require.Equal(t, kind, diagnostics.FirstUsableKind)
		})
	}
}
