package openai

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/stretchr/testify/require"
)

func TestChatPreOutputBufferPreservesFramesOnSuccess(t *testing.T) {
	ensureStreamTimeout(t)
	withFalseSuccessFallback(t, true)
	ctx, recorder, info := falseSuccessToggleStreamContext(t)
	frames := []string{
		`{"id":"chat","choices":[{"delta":{"role":"assistant"}}]}`,
		`{"id":"chat","choices":[{"delta":{"content":" \n"}}]}`,
		`{"id":"chat","choices":[{"delta":{"content":"pong"},"finish_reason":"stop"}]}`,
	}
	body := "data: " + strings.Join(frames, "\n\ndata: ") + "\n\ndata: [DONE]\n\n"
	_, err := OaiStreamHandler(ctx, info, responsesHTTPStream(body))
	require.Nil(t, err)
	for _, frame := range frames {
		require.Contains(t, recorder.Body.String(), frame)
	}
	value, ok := ctx.Get(helper.ContextKeyStreamOutputDiagnostics)
	require.True(t, ok)
	diagnostics := value.(helper.StreamOutputDiagnostics)
	require.Equal(t, "role", diagnostics.FirstFrameKind)
	require.Equal(t, "content", diagnostics.FirstUsableKind)
	require.Equal(t, 2, diagnostics.BufferedFrames)
	require.Equal(t, 6, diagnostics.ContentChars)
}

func TestChatPreOutputBufferIsBoundedAndNeverCommitsEmptyOutput(t *testing.T) {
	ensureStreamTimeout(t)
	withFalseSuccessFallback(t, true)
	for name, body := range map[string]string{
		"frames": strings.Repeat("data: {\"choices\":[{\"delta\":{\"content\":\" \"}}]}\n\n", maxChatPreOutputFrames+1),
		"bytes":  "data: {\"choices\":[{\"delta\":{\"content\":\"" + strings.Repeat(" ", maxChatPreOutputBytes) + "\"}}]}\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			ctx, recorder, info := falseSuccessToggleStreamContext(t)
			_, err := OaiStreamHandler(ctx, info, responsesHTTPStream(body))
			require.NotNil(t, err)
			require.Equal(t, "empty_response", string(err.GetErrorCode()))
			require.False(t, ctx.Writer.Written())
			require.Empty(t, recorder.Body.String())
			value, ok := ctx.Get(helper.ContextKeyStreamOutputDiagnostics)
			require.True(t, ok)
			require.True(t, value.(helper.StreamOutputDiagnostics).BufferLimitReached)
		})
	}
}

func TestChatStreamToolArgumentAndPolicyFramesRemainUsable(t *testing.T) {
	for _, frame := range []string{
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{"}}]}}]}`,
		`{"choices":[{"delta":{"function_call":{"arguments":"{"}}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"type":"custom","custom":{"input":"hello"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"content_filter"}]}`,
	} {
		require.True(t, chatStreamDataHasUsableOutput(frame))
	}
}

func TestChatPreOutputBufferPreservesToolEnvelopeAndArgumentWhitespace(t *testing.T) {
	ensureStreamTimeout(t)
	withFalseSuccessFallback(t, true)
	ctx, recorder, info := falseSuccessToggleStreamContext(t)
	frames := []string{
		`{"id":"chat","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_original","type":"function","function":{"name":"","arguments":""}}]}}]}`,
		`{"id":"chat","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"lookup","arguments":"{"}}]}}]}`,
		`{"id":"chat","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":" "}}]}}]}`,
		`{"id":"chat","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"}"}}]},"finish_reason":"tool_calls"}]}`,
	}
	body := "data: " + strings.Join(frames, "\n\ndata: ") + "\n\ndata: [DONE]\n\n"
	_, err := OaiStreamHandler(ctx, info, responsesHTTPStream(body))
	require.Nil(t, err)
	for _, frame := range frames {
		require.Contains(t, recorder.Body.String(), frame)
	}
	value, ok := ctx.Get(helper.ContextKeyStreamOutputDiagnostics)
	require.True(t, ok)
	diagnostics := value.(helper.StreamOutputDiagnostics)
	require.Equal(t, "tool_shell", diagnostics.FirstFrameKind)
	require.Equal(t, "tool_call", diagnostics.FirstUsableKind)
	require.Equal(t, 1, diagnostics.BufferedFrames)
}
