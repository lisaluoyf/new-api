package claude

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

func TestClaudeResponsesRequest(t *testing.T) {
	body := `{"model":"claude-opus-5","instructions":"Be concise","max_output_tokens":700,"stream":true,"reasoning":{"effort":"low"},"parallel_tool_calls":false,"tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}],"tool_choice":{"type":"function","name":"lookup"},"input":[{"role":"developer","content":"Answer in Chinese"},{"role":"user","content":[{"type":"input_text","text":"Find this","cache_control":{"type":"ephemeral"}},{"type":"input_image","image_url":"https://example.com/image.png"}]},{"type":"function_call","name":"lookup","call_id":"call_1","arguments":"{\"q\":\"hello\"}"},{"type":"function_call","name":"lookup","call_id":"call_2","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"one"},{"type":"function_call_output","call_id":"call_2","output":[{"type":"input_text","text":"two"}]}]}`
	var r dto.OpenAIResponsesRequest
	require.NoError(t, common.UnmarshalJsonStr(body, &r))
	out, err := responsesRequestToClaude(nil, r)
	require.NoError(t, err)
	encoded, err := common.Marshal(out)
	require.NoError(t, err)
	s := string(encoded)
	require.Equal(t, "low", gjson.Get(s, "output_config.effort").String())
	require.Equal(t, "adaptive", gjson.Get(s, "thinking.type").String())
	require.Equal(t, "summarized", gjson.Get(s, "thinking.display").String())
	require.EqualValues(t, 700, gjson.Get(s, "max_tokens").Int())
	require.False(t, gjson.Get(s, "temperature").Exists())
	require.True(t, gjson.Get(s, "tool_choice.disable_parallel_tool_use").Bool())
	require.Equal(t, "lookup", gjson.Get(s, "tool_choice.name").String())
	require.Len(t, out.System.([]dto.ClaudeMediaMessage), 2)
	require.Len(t, out.Messages, 3)
	require.Equal(t, "url", gjson.Get(s, "messages.0.content.1.source.type").String())
	require.Equal(t, "ephemeral", gjson.Get(s, "messages.0.content.0.cache_control.type").String())
	require.Equal(t, "call_2", gjson.Get(s, "messages.1.content.1.id").String())
	require.Equal(t, "call_2", gjson.Get(s, "messages.2.content.1.tool_use_id").String())
}
func TestClaudeResponsesRejectedRequests(t *testing.T) {
	cases := []string{
		`"previous_response_id":"resp_1"`, `"conversation":"conv_1"`, `"store":true`, `"background":true`,
		`"tools":[{"type":"file_search"}]`,
		`"text":{"format":{"type":"json_schema"}}`, `"max_output_tokens":0`, `"reasoning":{"effort":"bogus"}`,
		`"input":[{"type":"function_call_output","call_id":"missing","output":"hello"}]`,
		`"input":[{"type":"function_call","call_id":"call_1","name":"f","arguments":"bad"}]`,
		`"input":[{"role":"user","content":[{"type":"input_file","file_id":"file_1"}]}]`,
		`"include":["file_search_call.results"]`, `"truncation":"auto"`, `"top_logprobs":0`,
		`"tool_choice":{"type":"function","name":"unknown"}`, `"tools":[{"type":"function","name":"f"}],"tool_choice":"required"`, `"reasoning_effort":"low"`,
	}
	for _, v := range cases {
		t.Run(v, func(t *testing.T) {
			body := `{"model":"claude-opus-5-5","input":"hello",` + v + `}`
			var r dto.OpenAIResponsesRequest
			require.NoError(t, common.UnmarshalJsonStr(body, &r))
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
			defer common.CleanupBodyStorage(c)
			_, err := responsesRequestToClaude(c, r)
			require.Error(t, err)
			api := types.NewError(err, types.ErrorCodeConvertRequestFailed)
			require.Equal(t, 400, api.StatusCode)
			require.True(t, types.IsSkipRetryError(api))
		})
	}
}
func responsesClaudeContext(t *testing.T, stream bool) (*gin.Context, *httptest.ResponseRecorder, *relaycommon.RelayInfo) {
	old := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = old })
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	info := &relaycommon.RelayInfo{IsStream: stream, StartTime: time.Now(), OriginModelName: "claude-opus-5-5", RelayFormat: types.RelayFormatOpenAIResponses, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "upstream-mapped-name"}}
	return c, rec, info
}

const responsesClaudeUsage = `{"input_tokens":100,"output_tokens":40,"cache_read_input_tokens":200,"cache_creation_input_tokens":30,"cache_creation":{"ephemeral_5m_input_tokens":10,"ephemeral_1h_input_tokens":20},"output_tokens_details":{"thinking_tokens":15}}`

func TestClaudeResponsesNonstreamUsageAndRoundtrip(t *testing.T) {
	c, rec, info := responsesClaudeContext(t, false)
	body := `{"id":"msg_test","type":"message","role":"assistant","model":"private-name","stop_reason":"tool_use","content":[{"type":"thinking","thinking":"summary","signature":"secret_signature"},{"type":"text","text":"Looking up"},{"type":"tool_use","id":"call_1","name":"lookup","input":{"q":"hello"}},{"type":"tool_use","id":"call_2","name":"lookup","input":{}}],"usage":` + responsesClaudeUsage + `}`
	usage, err := (&Adaptor{}).DoResponse(c, &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, info)
	require.Nil(t, err)
	u := usage.(*dto.Usage)
	require.Equal(t, "anthropic", u.UsageSemantic)
	require.EqualValues(t, types.RelayFormatClaude, info.FinalRequestRelayFormat)
	require.Equal(t, 100, u.PromptTokens)
	require.Equal(t, 40, u.CompletionTokens)
	require.Equal(t, 200, u.PromptTokensDetails.CachedTokens)
	require.Equal(t, 30, u.PromptTokensDetails.CachedCreationTokens)
	require.Equal(t, 15, u.CompletionTokenDetails.ReasoningTokens)
	wire := rec.Body.String()
	require.Equal(t, "claude-opus-5-5", gjson.Get(wire, "model").String())
	require.NotContains(t, wire, "secret_signature")
	require.EqualValues(t, 330, gjson.Get(wire, "usage.input_tokens").Int())
	require.EqualValues(t, 40, gjson.Get(wire, "usage.output_tokens").Int())
	require.EqualValues(t, 370, gjson.Get(wire, "usage.total_tokens").Int())
	require.EqualValues(t, 15, gjson.Get(wire, "usage.output_tokens_details.reasoning_tokens").Int())
	require.Equal(t, `{"q":"hello"}`, gjson.Get(wire, "output.2.arguments").String())
	require.Equal(t, "{}", gjson.Get(wire, "output.3.arguments").String())
	// Actual Responses output can be supplied unchanged on a tool follow-up.
	var output []any
	require.NoError(t, common.UnmarshalJsonStr(gjson.Get(wire, "output").Raw, &output))
	history := []any{map[string]any{"role": "user", "content": "lookup"}}
	history = append(history, output...)
	history = append(history, map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "found"}, map[string]any{"type": "function_call_output", "call_id": "call_2", "output": "found"})
	input, e := common.Marshal(history)
	require.NoError(t, e)
	_, e = responsesRequestToClaude(nil, dto.OpenAIResponsesRequest{Model: "claude-opus-5-5", Input: input})
	require.NoError(t, e)
}
func responsesClaudeEvents(stop string) []string {
	return []string{
		`{"type":"message_start","message":{"id":"msg_test","model":"private-name","usage":` + responsesClaudeUsage + `}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"summary"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"secret"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Hello"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"call_1","name":"lookup","input":{}}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"q\":"}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"\"hello\"}"}}`,
		`{"type":"content_block_stop","index":2}`,
		`{"type":"message_delta","delta":{"stop_reason":"` + stop + `"},"usage":{"output_tokens":40}}`,
		`{"type":"message_stop"}`,
	}
}
func responsesClaudeSSE(events []string) string {
	return "data: " + strings.Join(events, "\n\ndata: ") + "\n\n"
}
func TestClaudeResponsesStreamEventsAndUsage(t *testing.T) {
	for _, stop := range []string{"end_turn", "tool_use", "max_tokens"} {
		t.Run(stop, func(t *testing.T) {
			c, rec, info := responsesClaudeContext(t, true)
			u, err := ClaudeResponsesHandler(c, &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(responsesClaudeSSE(responsesClaudeEvents(stop))))}, info)
			require.Nil(t, err)
			require.Equal(t, 100, u.PromptTokens)
			require.Equal(t, 40, u.CompletionTokens)
			require.Equal(t, 200, u.PromptTokensDetails.CachedTokens)
			require.Equal(t, 30, u.PromptTokensDetails.CachedCreationTokens)
			require.Equal(t, 15, u.CompletionTokenDetails.ReasoningTokens)
			text := rec.Body.String()
			require.NotContains(t, text, "[DONE]")
			require.NotContains(t, text, "secret")
			seq := 0
			seen := map[string]int{}
			var terminal gjson.Result
			for _, line := range strings.Split(text, "\n") {
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				e := gjson.Parse(strings.TrimPrefix(line, "data: "))
				require.EqualValues(t, seq, e.Get("sequence_number").Int())
				seq++
				seen[e.Get("type").String()]++
				if e.Get("response.status").String() == "completed" || e.Get("response.status").String() == "incomplete" {
					terminal = e.Get("response")
				}
			}
			require.Equal(t, 3, seen["response.output_item.added"])
			require.Equal(t, 3, seen["response.output_item.done"])
			require.Equal(t, 2, seen["response.function_call_arguments.delta"])
			require.Equal(t, 1, seen["response.function_call_arguments.done"])
			require.EqualValues(t, 330, terminal.Get("usage.input_tokens").Int())
			require.EqualValues(t, 370, terminal.Get("usage.total_tokens").Int())
			require.Equal(t, `{"q":"hello"}`, terminal.Get("output.2.arguments").String())
			require.True(t, info.StreamStatus.HasTerminalUsage())
			if stop == "max_tokens" {
				require.Equal(t, "max_output_tokens", terminal.Get("incomplete_details.reason").String())
			}
		})
	}
}
func TestClaudeResponsesBrokenStreamsCannotSettle(t *testing.T) {
	base := responsesClaudeEvents("end_turn")
	cases := map[string][]string{"truncated": base[:len(base)-1], "no usage": append(append([]string{}, base[:len(base)-2]...), `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`, `{"type":"message_stop"}`), "bad json": {"oops"}, "upstream error": {`{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`}, "lifecycle only": {base[0], base[len(base)-2], base[len(base)-1]}, "invalid arguments": append(append([]string{}, base[:10]...), `{"type":"content_block_stop","index":2}`, base[len(base)-2], base[len(base)-1])}
	for name, events := range cases {
		t.Run(name, func(t *testing.T) {
			c, rec, info := responsesClaudeContext(t, true)
			u, err := ClaudeResponsesHandler(c, &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(responsesClaudeSSE(events)))}, info)
			require.NotNil(t, err)
			require.Nil(t, u)
			require.NotContains(t, rec.Body.String(), "event: response.completed")
		})
	}
}
func TestClaudeResponsesCanceledStreamCannotSettle(t *testing.T) {
	c, _, info := responsesClaudeContext(t, true)
	ctx, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(ctx)
	u, err := ClaudeResponsesHandler(c, &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(responsesClaudeSSE(responsesClaudeEvents("end_turn"))))}, info)
	require.NotNil(t, err)
	require.Nil(t, u)
}

func TestClaudeResponsesStrictSchemaAndSignedThinking(t *testing.T) {
	var r dto.OpenAIResponsesRequest
	require.NoError(t, common.UnmarshalJsonStr(`{"model":"claude-opus-5-5","input":"Extract a number","include":["reasoning.encrypted_content"],"reasoning":{"effort":"low"},"tools":[{"type":"function","name":"lookup","strict":true,"parameters":{"type":"object","properties":{},"additionalProperties":false}}],"text":{"format":{"type":"json_schema","name":"answer","strict":true,"schema":{"type":"object","properties":{"answer":{"type":"integer"}},"required":["answer"],"additionalProperties":false}}}}`, &r))
	out, err := responsesRequestToClaude(nil, r)
	require.NoError(t, err)
	data, err := common.Marshal(out)
	require.NoError(t, err)
	require.True(t, gjson.GetBytes(data, "tools.0.strict").Bool())
	require.Equal(t, "low", gjson.GetBytes(data, "output_config.effort").String())
	require.Equal(t, "integer", gjson.GetBytes(data, "output_config.format.schema.properties.answer.type").String())
	for _, typ := range []string{"thinking", "redacted_thinking"} {
		t.Run(typ, func(t *testing.T) {
			s := newClaudeResponsesState(&relaycommon.RelayInfo{OriginModelName: "claude-opus-5-5"})
			b := dto.ClaudeMediaMessage{Type: typ, Thinking: common.GetPointer("summary"), Signature: "real_upstream_signature"}
			if typ == "redacted_thinking" {
				b = dto.ClaudeMediaMessage{Type: typ, Data: []byte(`"opaque_redacted_data"`)}
			}
			require.NoError(t, s.startBlock(nil, 0, b, false))
			require.NoError(t, s.stopBlock(nil, 0, false))
			encoded, err := common.Marshal([]any{map[string]any{"role": "user", "content": "question"}, s.blocks[0].item, map[string]any{"type": "function_call", "name": "lookup", "call_id": "call_1", "arguments": "{}"}, map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "answer"}})
			require.NoError(t, err)
			out, err := responsesRequestToClaude(nil, dto.OpenAIResponsesRequest{Model: "claude-opus-5-5", Input: encoded})
			require.NoError(t, err)
			got := out.Messages[1].Content.([]dto.ClaudeMediaMessage)[0]
			require.Equal(t, typ, got.Type)
			require.Equal(t, b.Signature, got.Signature)
			require.Equal(t, b.Data, got.Data)
			if b.Thinking != nil {
				require.Equal(t, *b.Thinking, *got.Thinking)
			}
		})
	}
}

func TestClaudeResponsesExplicitZeroTerminalUsage(t *testing.T) {
	c, rec, info := responsesClaudeContext(t, true)
	events := []string{`{"type":"message_start","message":{"id":"msg_zero","usage":{"input_tokens":100,"output_tokens":1}}}`, `{"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":0}}`, `{"type":"message_stop"}`}
	u, err := ClaudeResponsesHandler(c, &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(responsesClaudeSSE(events)))}, info)
	require.Nil(t, err)
	require.Zero(t, u.CompletionTokens)
	require.Contains(t, rec.Body.String(), "event: response.incomplete")
}

func TestClaudeResponsesInvalidNonstreamCannotSettle(t *testing.T) {
	for _, body := range []string{`{}`, `not json`, `{"type":"message","role":"assistant","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn"}`, `{"type":"message","role":"assistant","content":[],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":1}}`, `{"type":"message","role":"assistant","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":-1,"output_tokens":1}}`} {
		c, rec, info := responsesClaudeContext(t, false)
		u, err := ClaudeResponsesHandler(c, &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, info)
		require.NotNil(t, err)
		require.Nil(t, u)
		require.Empty(t, rec.Body.String())
	}
}

type claudeResponsesHoldingBody struct {
	*strings.Reader
	closed chan struct{}
	once   sync.Once
}

func (b *claudeResponsesHoldingBody) Read(p []byte) (int, error) {
	n, e := b.Reader.Read(p)
	if e == io.EOF {
		<-b.closed
		return 0, context.Canceled
	}
	return n, e
}
func (b *claudeResponsesHoldingBody) Close() error { b.once.Do(func() { close(b.closed) }); return nil }

type claudeResponsesCancelWriter struct {
	gin.ResponseWriter
	cancel context.CancelFunc
}

func (w *claudeResponsesCancelWriter) Write(p []byte) (int, error) {
	n, e := w.ResponseWriter.Write(p)
	if strings.Contains(string(p), "event: response.completed") {
		w.cancel()
	}
	return n, e
}
func TestClaudeResponsesTerminalCancellationSettlesOnce(t *testing.T) {
	c, _, info := responsesClaudeContext(t, true)
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	c.Writer = &claudeResponsesCancelWriter{ResponseWriter: c.Writer, cancel: cancel}
	body := &claudeResponsesHoldingBody{Reader: strings.NewReader(responsesClaudeSSE(responsesClaudeEvents("end_turn"))), closed: make(chan struct{})}
	timer := time.AfterFunc(2*time.Second, func() { cancel(); _ = body.Close() })
	defer timer.Stop()
	start := time.Now()
	u, err := ClaudeResponsesHandler(c, &http.Response{StatusCode: 200, Body: body}, info)
	require.Nil(t, err)
	require.Less(t, time.Since(start), time.Second)
	require.Equal(t, 40, u.CompletionTokens)
	require.True(t, info.StreamStatus.HasTerminalUsage())
}

func TestClaudeResponsesToolIntegersRemainExact(t *testing.T) {
	c, rec, info := responsesClaudeContext(t, false)
	body := `{"type":"message","role":"assistant","content":[{"type":"tool_use","id":"call_big","name":"lookup","input":{"id":1234567890123456789}}],"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":20}}`
	_, err := ClaudeResponsesHandler(c, &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, info)
	require.Nil(t, err)
	args := gjson.Get(rec.Body.String(), "output.0.arguments").String()
	require.Contains(t, args, "1234567890123456789")
	history := []any{map[string]any{"role": "user", "content": "lookup"}, map[string]any{"type": "function_call", "call_id": "call_big", "name": "lookup", "arguments": args}, map[string]any{"type": "function_call_output", "call_id": "call_big", "output": "found"}}
	input, e := common.Marshal(history)
	require.NoError(t, e)
	out, e := responsesRequestToClaude(nil, dto.OpenAIResponsesRequest{Model: "claude-opus-5-5", Input: input})
	require.NoError(t, e)
	encoded, e := common.Marshal(out)
	require.NoError(t, e)
	require.Contains(t, string(encoded), "1234567890123456789")
}

func TestClaudeResponsesPartialCancellationUsesReportedInput(t *testing.T) {
	c, rec, info := responsesClaudeContext(t, true)
	info.UserId = 123
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	c.Writer = &cancelOnClaudeFrameWriter{c.Writer, rec, "response.output_text.delta", cancel}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	go func() { _, _ = io.WriteString(writer, responsesClaudeSSE(responsesClaudeEvents("end_turn")[:7])) }()
	u, err := ClaudeResponsesHandler(c, &http.Response{StatusCode: 200, Body: reader}, info)
	require.Nil(t, err)
	require.Equal(t, 100, u.PromptTokens)
	require.Equal(t, 200, u.PromptTokensDetails.CachedTokens)
	require.Equal(t, "upstream_reported_partial", u.UsageSource)
	require.NotNil(t, info.CanceledStreamUsage)
}
