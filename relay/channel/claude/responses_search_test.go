package claude

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestClaudeResponsesOriginalClientShape(t *testing.T) {
	var r dto.OpenAIResponsesRequest
	require.NoError(t, common.UnmarshalJsonStr(`{"model":"claude-opus-5-5","input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}],"instructions":"Help me","store":false,"stream":true,"parallel_tool_calls":true,"reasoning":{"effort":"high","summary":"auto"},"text":{"verbosity":"low"},"prompt_cache_key":"client-session","include":["reasoning.encrypted_content"],"tool_choice":"auto","tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{}}},{"type":"web_search","search_context_size":"medium"}]}`, &r))
	out, e := responsesRequestToClaude(nil, r)
	require.NoError(t, e)
	encoded, e := common.Marshal(out)
	require.NoError(t, e)
	require.Equal(t, "web_search_20250305", gjson.GetBytes(encoded, "tools.1.type").String())
	require.Equal(t, "ephemeral", gjson.GetBytes(encoded, "cache_control.type").String())
	require.Contains(t, gjson.GetBytes(encoded, "system.1.text").String(), "concise")
}
func TestClaudeResponsesWebSearchCitationsAndReplay(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "sse"}[stream], func(t *testing.T) {
			c, rec, info := responsesClaudeContext(t, stream)
			usage := `{"input_tokens":100,"output_tokens":40,"server_tool_use":{"web_search_requests":2}}`
			content := `[{"type":"server_tool_use","id":"srv_1","name":"web_search","input":{"query":"Claude docs"}},{"type":"web_search_tool_result","tool_use_id":"srv_1","content":[{"type":"web_search_result","url":"https://platform.claude.com/docs","title":"Docs","encrypted_content":"provider_search_cipher"}]},{"type":"text","text":"See the docs","citations":[{"type":"web_search_result_location","url":"https://platform.claude.com/docs","title":"Docs","encrypted_index":"provider_citation_cipher","cited_text":"docs"}]}]`
			body := `{"type":"message","role":"assistant","content":` + content + `,"stop_reason":"end_turn","usage":` + usage + `}`
			if stream {
				body = responsesClaudeSSE([]string{
					`{"type":"message_start","message":{"id":"msg_search","usage":` + usage + `}}`,
					`{"type":"content_block_start","index":0,"content_block":{"type":"server_tool_use","id":"srv_1","name":"web_search","input":{}}}`,
					`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"Claude docs\"}"}}`,
					`{"type":"content_block_stop","index":0}`,
					`{"type":"content_block_start","index":1,"content_block":` + gjson.Get(content, "1").Raw + `}`, `{"type":"content_block_stop","index":1}`,
					`{"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}`,
					`{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"See the docs"}}`,
					`{"type":"content_block_delta","index":2,"delta":{"type":"citations_delta","citation":` + gjson.Get(content, "2.citations.0").Raw + `}}`,
					`{"type":"content_block_stop","index":2}`,
					`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":40,"server_tool_use":{"web_search_requests":2}}}`, `{"type":"message_stop"}`})
			}
			u, e := ClaudeResponsesHandler(c, &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, info)
			require.Nil(t, e)
			require.Equal(t, 40, u.CompletionTokens)
			require.Equal(t, 2, c.GetInt("claude_web_search_requests"))
			wire := rec.Body.String()
			if stream {
				for _, line := range strings.Split(wire, "\n") {
					if strings.HasPrefix(line, "data: ") {
						v := gjson.Parse(strings.TrimPrefix(line, "data: "))
						if v.Get("type").String() == "response.completed" {
							wire = v.Get("response").Raw
						}
					}
				}
				require.Contains(t, rec.Body.String(), "event: response.output_text.annotation.added")
				require.Contains(t, rec.Body.String(), "event: response.web_search_call.completed")
			}
			require.Equal(t, "web_search_call", gjson.Get(wire, "output.0.type").String())
			require.Equal(t, "completed", gjson.Get(wire, "output.0.status").String())
			require.Equal(t, "https://platform.claude.com/docs", gjson.Get(wire, "output.0.action.sources.0.url").String())
			require.Equal(t, "url_citation", gjson.Get(wire, "output.2.content.0.annotations.0.type").String())
			require.Contains(t, gjson.Get(wire, "output.3.encrypted_content").String(), "anthropic_turn_v1:")
			var output []any
			require.NoError(t, common.UnmarshalJsonStr(gjson.Get(wire, "output").Raw, &output))
			history := []any{map[string]any{"role": "user", "content": "search docs"}}
			history = append(history, output...)
			history = append(history, map[string]any{"role": "user", "content": "Summarize that result"})
			raw, e2 := common.Marshal(history)
			require.NoError(t, e2)
			request, e2 := responsesRequestToClaude(nil, dto.OpenAIResponsesRequest{Model: "claude-opus-5-5", Input: raw})
			require.NoError(t, e2)
			encoded, e2 := common.Marshal(request.Messages[1].Content)
			require.NoError(t, e2)
			require.JSONEq(t, content, string(encoded))
		})
	}
}
