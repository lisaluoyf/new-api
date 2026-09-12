package openai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResponsesStreamEventHasUsableOutput(t *testing.T) {
	require.False(t, responsesStreamEventHasUsableOutput(dto.ResponsesStreamResponse{Type: "response.created"}))
	require.False(t, responsesStreamEventHasUsableOutput(dto.ResponsesStreamResponse{Type: "response.in_progress"}))
	require.False(t, responsesStreamEventHasUsableOutput(dto.ResponsesStreamResponse{
		Type: "response.output_item.added", Item: &dto.ResponsesOutput{Type: "message"},
	}))
	require.False(t, responsesStreamEventHasUsableOutput(dto.ResponsesStreamResponse{
		Type: "response.output_item.added", Item: &dto.ResponsesOutput{Type: "function_call", Name: "lookup"},
	}))
	require.True(t, responsesStreamEventHasUsableOutput(dto.ResponsesStreamResponse{
		Type: "response.output_text.delta", Delta: "hello",
	}))
	require.True(t, responsesStreamEventHasUsableOutput(dto.ResponsesStreamResponse{
		Type: "response.function_call_arguments.delta", Delta: `{"city":`,
	}))
	require.True(t, responsesStreamEventHasUsableOutput(dto.ResponsesStreamResponse{
		Type: "response.refusal.done", Refusal: "I cannot help with that.",
	}))
}

func TestResponsesResponseValidEmptyTerminal(t *testing.T) {
	require.True(t, responsesResponseHasValidEmptyTerminal(&dto.OpenAIResponsesResponse{
		IncompleteDetails: &dto.IncompleteDetails{Reason: "content_filter"},
	}))
	require.False(t, responsesResponseHasValidEmptyTerminal(&dto.OpenAIResponsesResponse{
		IncompleteDetails: &dto.IncompleteDetails{Reason: "steered"},
	}))
	require.False(t, responsesResponseHasValidEmptyTerminal(&dto.OpenAIResponsesResponse{
		IncompleteDetails: &dto.IncompleteDetails{Reason: "max_output_tokens"},
	}))
}

func TestResponsesBackgroundResponseCanBeReturnedWithoutOutput(t *testing.T) {
	require.True(t, responsesResponseCanBeReturnedWithoutOutput(&dto.OpenAIResponsesResponse{
		Status: json.RawMessage(`"queued"`), Background: true,
	}))
	require.False(t, responsesResponseCanBeReturnedWithoutOutput(&dto.OpenAIResponsesResponse{
		Status: json.RawMessage(`"completed"`), Background: true,
	}))
}

func responsesStreamTestContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c, recorder
}

func responsesStreamTestInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		StartTime:       time.Now(),
		OriginModelName: "gpt-test",
		RelayFormat:     types.RelayFormatOpenAIResponses,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-test",
		},
	}
}

func responsesHTTPStream(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}
}

func TestOaiResponsesStreamHandlerRejectsLifecycleOnlyHTTP200(t *testing.T) {
	c, recorder := responsesStreamTestContext(t)
	body := "data: {\"type\":\"response.created\",\"response\":{\"status\":\"in_progress\"}}\n\n" +
		"data: {\"type\":\"response.in_progress\",\"response\":{\"status\":\"in_progress\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n"

	_, err := OaiResponsesStreamHandler(c, responsesStreamTestInfo(), responsesHTTPStream(body))
	require.NotNil(t, err)
	require.Equal(t, types.ErrorCodeEmptyResponse, err.GetErrorCode())
	require.False(t, c.Writer.Written())
	require.Empty(t, recorder.Body.String())
}

func TestOaiResponsesStreamHandlerRejectsDoneWithoutTerminalOrOutput(t *testing.T) {
	c, recorder := responsesStreamTestContext(t)
	body := "data: {\"type\":\"response.created\",\"response\":{\"status\":\"in_progress\"}}\n\n" +
		"data: [DONE]\n\n"

	_, err := OaiResponsesStreamHandler(c, responsesStreamTestInfo(), responsesHTTPStream(body))
	require.NotNil(t, err)
	require.Equal(t, types.ErrorCodeBadResponse, err.GetErrorCode())
	require.False(t, c.Writer.Written())
	require.Empty(t, recorder.Body.String())
}

func TestOaiResponsesStreamHandlerAcceptsRealOutput(t *testing.T) {
	c, recorder := responsesStreamTestContext(t)
	body := "data: {\"type\":\"response.created\",\"response\":{\"status\":\"in_progress\"}}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello\"}]}],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"

	usage, err := OaiResponsesStreamHandler(c, responsesStreamTestInfo(), responsesHTTPStream(body))
	require.Nil(t, err)
	require.NotNil(t, usage)
	require.True(t, c.Writer.Written())
	require.Contains(t, recorder.Body.String(), "response.output_text.delta")
	require.Contains(t, recorder.Body.String(), "hello")
}

func TestOaiResponsesHandlerRejectsEmptyHTTP200(t *testing.T) {
	c, recorder := responsesStreamTestContext(t)
	resp := responsesHTTPStream(`{"id":"resp_empty","status":"completed","output":[],"usage":null}`)

	_, err := OaiResponsesHandler(c, responsesStreamTestInfo(), resp)
	require.NotNil(t, err)
	require.Equal(t, types.ErrorCodeEmptyResponse, err.GetErrorCode())
	require.False(t, c.Writer.Written())
	require.Empty(t, recorder.Body.String())
}

func TestOaiResponsesToChatHandlerRejectsEmptyHTTP200(t *testing.T) {
	c, recorder := responsesStreamTestContext(t)
	info := responsesStreamTestInfo()
	info.RelayFormat = types.RelayFormatOpenAI
	resp := responsesHTTPStream(`{"id":"resp_empty","status":"completed","output":[],"usage":null}`)

	_, err := OaiResponsesToChatHandler(c, info, resp)
	require.NotNil(t, err)
	require.Equal(t, types.ErrorCodeEmptyResponse, err.GetErrorCode())
	require.False(t, c.Writer.Written())
	require.Empty(t, recorder.Body.String())
}

func TestOaiResponsesToChatStreamHandlerRejectsLifecycleOnlyHTTP200(t *testing.T) {
	c, recorder := responsesStreamTestContext(t)
	info := responsesStreamTestInfo()
	info.RelayFormat = types.RelayFormatOpenAI
	body := "data: {\"type\":\"response.created\",\"response\":{\"status\":\"in_progress\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n"

	_, err := OaiResponsesToChatStreamHandler(c, info, responsesHTTPStream(body))
	require.NotNil(t, err)
	require.Equal(t, types.ErrorCodeEmptyResponse, err.GetErrorCode())
	require.False(t, c.Writer.Written())
	require.Empty(t, recorder.Body.String())
}

func TestOaiResponsesToChatStreamHandlerAcceptsTerminalOnlyText(t *testing.T) {
	c, recorder := responsesStreamTestContext(t)
	info := responsesStreamTestInfo()
	info.RelayFormat = types.RelayFormatOpenAI
	body := "data: {\"type\":\"response.created\",\"response\":{\"status\":\"in_progress\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"terminal hello\"}]}],\"usage\":{\"input_tokens\":1,\"output_tokens\":2,\"total_tokens\":3}}}\n\n"

	_, err := OaiResponsesToChatStreamHandler(c, info, responsesHTTPStream(body))
	require.Nil(t, err)
	require.Contains(t, recorder.Body.String(), "terminal hello")
}

func TestOaiResponsesStreamHandlerAcceptsContentFilterTerminal(t *testing.T) {
	c, recorder := responsesStreamTestContext(t)
	body := "data: {\"type\":\"response.created\",\"response\":{\"status\":\"in_progress\"}}\n\n" +
		"data: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\",\"output\":[],\"incomplete_details\":{\"reason\":\"content_filter\"}}}\n\n"

	_, err := OaiResponsesStreamHandler(c, responsesStreamTestInfo(), responsesHTTPStream(body))
	require.Nil(t, err)
	require.Contains(t, recorder.Body.String(), "response.incomplete")
	require.Contains(t, recorder.Body.String(), "content_filter")
}

func TestResponsesResponseHasUsableOutput(t *testing.T) {
	require.False(t, responsesResponseHasUsableOutput(&dto.OpenAIResponsesResponse{}))
	require.False(t, responsesResponseHasUsableOutput(&dto.OpenAIResponsesResponse{
		Output: []dto.ResponsesOutput{{Type: "message"}},
	}))
	require.True(t, responsesResponseHasUsableOutput(&dto.OpenAIResponsesResponse{
		Output: []dto.ResponsesOutput{{Type: "message", Content: []dto.ResponsesOutputContent{{Type: "output_text", Text: "ok"}}}},
	}))
	require.True(t, responsesResponseHasUsableOutput(&dto.OpenAIResponsesResponse{
		Output: []dto.ResponsesOutput{{Type: "function_call", Name: "lookup", Arguments: json.RawMessage(`"{}"`)}},
	}))
}
