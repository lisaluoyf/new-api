package gemini

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func geminiResponsesFixture(stream bool) (*gin.Context, *httptest.ResponseRecorder, *relaycommon.RelayInfo) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	return c, rec, &relaycommon.RelayInfo{UserId: 123, IsStream: stream, StartTime: time.Now(), RelayFormat: types.RelayFormatOpenAIResponses, OriginModelName: "gemini-4-argon", ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-4-argon", ChannelType: constant.ChannelTypeGemini}}
}
func parseGeminiResponsesRequest(t *testing.T, raw string) dto.OpenAIResponsesRequest {
	t.Helper()
	var r dto.OpenAIResponsesRequest
	require.NoError(t, common.UnmarshalJsonStr(raw, &r))
	return r
}
func TestGeminiResponsesRequest(t *testing.T) {
	_, _, info := geminiResponsesFixture(false)
	r := parseGeminiResponsesRequest(t, `{"model":"gemini-4-argon","instructions":"Be precise","input":[{"role":"user","content":[{"type":"input_text","text":"Hello"}]}],"temperature":0,"top_p":0,"max_output_tokens":2048,"store":false}`)
	out, err := responsesRequestToGemini(nil, info, r)
	require.NoError(t, err)
	require.Equal(t, "Hello", out.Contents[0].Parts[0].Text)
	require.Equal(t, "Be precise", out.SystemInstructions.Parts[0].Text)
	require.NotNil(t, out.GenerationConfig.Temperature)
	require.Equal(t, 0.0, *out.GenerationConfig.Temperature)
	require.NotNil(t, out.GenerationConfig.TopP)
	require.Equal(t, 0.0, *out.GenerationConfig.TopP)
	r = parseGeminiResponsesRequest(t, `{"model":"gemini-4-argon","input":"Search","tools":[{"type":"web_search"}]}`)
	out, err = responsesRequestToGemini(nil, info, r)
	require.NoError(t, err)
	require.NotNil(t, out.GetTools()[0].GoogleSearch)
}
func TestGeminiResponsesFunctionRoundTrip(t *testing.T) {
	_, _, info := geminiResponsesFixture(false)
	r := parseGeminiResponsesRequest(t, `{"model":"gemini-4-argon","input":[{"role":"user","content":"Weather?"},{"type":"function_call","call_id":"call_weather","name":"weather","arguments":"{\"city\":\"Paris\"}"},{"type":"function_call_output","call_id":"call_weather","output":"Sunny"}],"tools":[{"type":"function","name":"weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}],"tool_choice":{"type":"function","name":"weather"}}`)
	out, err := responsesRequestToGemini(nil, info, r)
	require.NoError(t, err)
	require.Len(t, out.Contents, 3)
	require.Equal(t, "weather", out.Contents[1].Parts[0].FunctionCall.FunctionName)
	require.Equal(t, "weather", out.Contents[2].Parts[0].FunctionResponse.Name)
	require.Equal(t, dto.FunctionCallingConfigMode("ANY"), out.ToolConfig.FunctionCallingConfig.Mode)
	require.Equal(t, []string{"weather"}, out.ToolConfig.FunctionCallingConfig.AllowedFunctionNames)
}
func TestGeminiResponsesRejectsUnsupportedStateAndMalformedInput(t *testing.T) {
	_, _, info := geminiResponsesFixture(false)
	for _, extra := range []string{`"previous_response_id":"resp_old"`, `"store":true`, `"conversation":"old"`, `"tools":[{"type":"file_search"}]`, `"max_output_tokens":0`, `"parallel_tool_calls":false`, `"tools":[{"type":"function","name":"googleSearch"}]`, `"tools":[{"type":"web_search","filters":{"allowed_domains":["example.com"]}}]`} {
		t.Run(extra, func(t *testing.T) {
			r := parseGeminiResponsesRequest(t, `{"model":"gemini-4-argon","input":"Hello",`+extra+`}`)
			_, err := responsesRequestToGemini(nil, info, r)
			require.Error(t, err)
			var apiErr *types.NewAPIError
			require.ErrorAs(t, err, &apiErr)
			require.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
		})
	}
	r := parseGeminiResponsesRequest(t, `{"input":[{"type":"function_call_output","call_id":"unknown","output":"Sunny"}]}`)
	_, err := responsesRequestToGemini(nil, info, r)
	require.Error(t, err)
}
func TestGeminiResponsesNonStreamUsageAndGrounding(t *testing.T) {
	c, rec, info := geminiResponsesFixture(false)
	body := `{"candidates":[{"content":{"parts":[{"text":"Hello"}]},"finishReason":"STOP","groundingMetadata":{"webSearchQueries":["hello"],"groundingChunks":[{"web":{"uri":"https://example.com","title":"Example"}}],"groundingSupports":[{"segment":{"startIndex":0,"endIndex":5},"groundingChunkIndices":[0]}]}}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3,"thoughtsTokenCount":2,"totalTokenCount":15,"cachedContentTokenCount":4}}`
	u, apiErr := GeminiResponsesHandler(c, info, &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))})
	require.Nil(t, apiErr)
	require.Equal(t, 10, u.PromptTokens)
	require.Equal(t, 5, u.CompletionTokens)
	wire := rec.Body.String()
	require.Equal(t, "response", gjson.Get(wire, "object").String())
	require.Equal(t, "completed", gjson.Get(wire, "status").String())
	require.Equal(t, "web_search_call", gjson.Get(wire, "output.0.type").String())
	require.Equal(t, "Hello", gjson.Get(wire, "output.1.content.0.text").String())
	require.Equal(t, "https://example.com", gjson.Get(wire, "output.1.content.0.annotations.0.url").String())
	require.Equal(t, int64(4), gjson.Get(wire, "usage.input_tokens_details.cached_tokens").Int())
	require.Equal(t, int64(2), gjson.Get(wire, "usage.output_tokens_details.reasoning_tokens").Int())
}
func TestGeminiResponsesStreamCompletionAndEOF(t *testing.T) {
	old := constant.StreamingTimeout
	constant.StreamingTimeout = 5
	t.Cleanup(func() { constant.StreamingTimeout = old })
	for _, terminal := range []bool{true, false} {
		t.Run(fmt.Sprint(terminal), func(t *testing.T) {
			c, rec, info := geminiResponsesFixture(true)
			body := "data: " + `{"candidates":[{"content":{"parts":[{"text":"Hello"}]}}]}` + "\n\n"
			if terminal {
				body += "data: " + `{"candidates":[{"content":{"parts":[]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3,"totalTokenCount":13}}` + "\n\n"
			}
			u, e := GeminiResponsesHandler(c, info, &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))})
			if terminal {
				require.Nil(t, e)
				require.Equal(t, 13, u.TotalTokens)
				require.Contains(t, rec.Body.String(), "event: response.completed")
			} else {
				require.NotNil(t, e)
				require.Nil(t, u)
				require.NotContains(t, rec.Body.String(), "event: response.completed")
				require.Contains(t, rec.Body.String(), "event: response.failed")
			}
			seq := int64(0)
			for _, line := range strings.Split(rec.Body.String(), "\n") {
				if strings.HasPrefix(line, "data: {") {
					data := strings.TrimPrefix(line, "data: ")
					require.Equal(t, seq, gjson.Get(data, "sequence_number").Int())
					seq++
				}
			}
			require.Greater(t, seq, int64(3))
		})
	}
}
func TestGeminiResponsesFunctionOutputAndTruncation(t *testing.T) {
	for _, finish := range []string{"STOP", "MAX_TOKENS"} {
		t.Run(finish, func(t *testing.T) {
			c, rec, info := geminiResponsesFixture(false)
			body := fmt.Sprintf(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"weather","args":{"city":"Paris"}}}]},"finishReason":%q}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3,"totalTokenCount":13}}`, finish)
			_, e := GeminiResponsesHandler(c, info, &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))})
			require.Nil(t, e)
			wire := rec.Body.String()
			require.Equal(t, "function_call", gjson.Get(wire, "output.0.type").String())
			require.Equal(t, "weather", gjson.Get(wire, "output.0.name").String())
			require.Equal(t, `{"city":"Paris"}`, gjson.Get(wire, "output.0.arguments").String())
			if finish == "MAX_TOKENS" {
				require.Equal(t, "incomplete", gjson.Get(wire, "status").String())
				require.Equal(t, "max_output_tokens", gjson.Get(wire, "incomplete_details.reason").String())
			}
		})
	}
}
func TestGeminiResponsesEmptyDoesNotSucceed(t *testing.T) {
	c, rec, info := geminiResponsesFixture(false)
	_, e := GeminiResponsesHandler(c, info, &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[]},"finishReason":"STOP"}]}`))})
	require.NotNil(t, e)
	require.Empty(t, rec.Body.String())
}
func TestGemini4AssistantContinuationPreservesHistory(t *testing.T) {
	c, _, info := geminiResponsesFixture(false)
	r := dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "Hello"}, {Role: "assistant", Content: "Start"}}}
	out, err := CovertOpenAI2Gemini(c, r, info)
	require.NoError(t, err)
	require.Len(t, out.Contents, 3)
	require.Equal(t, "model", out.Contents[1].Role)
	require.Equal(t, "Start", out.Contents[1].Parts[0].Text)
	require.Equal(t, "user", out.Contents[2].Role)
	require.Len(t, r.Messages, 2)
}
func TestGeminiSchemaInfersObjectsAndRemovesInapplicableProperties(t *testing.T) {
	schema := map[string]any{"properties": map[string]any{"text": map[string]any{"type": "string", "properties": map[string]any{"invalid": map[string]any{"type": "string"}}}}}
	cleaned := cleanFunctionParameters(schema).(map[string]any)
	require.Equal(t, "OBJECT", cleaned["type"])
	prop := cleaned["properties"].(map[string]any)["text"].(map[string]any)
	require.NotContains(t, prop, "properties")
	require.Contains(t, schema["properties"].(map[string]any)["text"], "properties")
}

func TestGeminiResponsesCancellationUsesOnlyReportedPartialUsage(t *testing.T) {
	old := constant.StreamingTimeout
	constant.StreamingTimeout = 5
	t.Cleanup(func() { constant.StreamingTimeout = old })
	for _, reported := range []bool{true, false} {
		t.Run(fmt.Sprint(reported), func(t *testing.T) {
			c, rec, info := geminiResponsesFixture(true)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c.Request = c.Request.WithContext(ctx)
			c.Writer = &cancelGeminiWriter{c.Writer, cancel}
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			payload := `{"candidates":[{"content":{"parts":[{"text":"hello"}]}}]`
			if reported {
				payload += `,"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3,"totalTokenCount":13}`
			}
			payload += "}"
			go func() { _, _ = io.WriteString(writer, "data: "+payload+"\n\n") }()
			u, e := GeminiResponsesHandler(c, info, &http.Response{StatusCode: 200, Body: reader})
			if reported {
				require.Nil(t, e)
				require.Equal(t, "upstream_reported_partial", u.UsageSource)
				require.Equal(t, 13, u.TotalTokens)
			} else {
				require.NotNil(t, e)
				require.Nil(t, u)
			}
			require.NotContains(t, rec.Body.String(), "event: response.completed")
		})
	}
}
