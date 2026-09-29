package claude

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestClaudeThinkingUsageNonstream(t *testing.T) {
	for _, tc := range []struct {
		name, details string
		want          int
	}{
		{"reported", `,"output_tokens_details":{"thinking_tokens":833}`, 833},
		{"explicit zero", `,"output_tokens_details":{"thinking_tokens":0}`, 0},
		{"legacy missing", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, format := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude} {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
				info := &relaycommon.RelayInfo{OriginModelName: "claude-sonnet-5-5", ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "claude-sonnet-5-5"}, RelayFormat: format}
				state := &ClaudeResponseInfo{}
				body := []byte(fmt.Sprintf(`{"id":"msg_test","type":"message","model":"claude-sonnet-5-5","role":"assistant","content":[{"type":"thinking","thinking":""},{"type":"text","text":"423"}],"stop_reason":"end_turn","usage":{"input_tokens":100,"output_tokens":1409%s}}`, tc.details))
				require.Nil(t, HandleClaudeResponseData(c, info, state, &http.Response{StatusCode: 200, Header: make(http.Header)}, body))
				require.Equal(t, tc.want, state.Usage.CompletionTokenDetails.ReasoningTokens)
				require.Equal(t, 1409, state.Usage.CompletionTokens)
				require.Equal(t, 1509, state.Usage.TotalTokens)
				if format == types.RelayFormatOpenAI {
					require.EqualValues(t, tc.want, gjson.Get(recorder.Body.String(), "usage.completion_tokens_details.reasoning_tokens").Int())
					require.EqualValues(t, 1409, gjson.Get(recorder.Body.String(), "usage.completion_tokens").Int())
				} else {
					require.JSONEq(t, string(body), recorder.Body.String())
				}
			}
		})
	}
}

func TestClaudeThinkingUsageStreaming(t *testing.T) {
	state := &ClaudeResponseInfo{}
	for _, tc := range []struct {
		body string
		want int
	}{
		{`{"type":"message_start","message":{"id":"msg_test","usage":{"input_tokens":100,"output_tokens":1,"output_tokens_details":{"thinking_tokens":1}}}}`, 1},
		{`{"type":"message_delta","usage":{"output_tokens":100,"output_tokens_details":{"thinking_tokens":40}}}`, 40},
		{`{"type":"message_delta","usage":{"output_tokens":1409,"output_tokens_details":{"thinking_tokens":833}}}`, 833},
		{`{"type":"message_delta","usage":{"output_tokens":1409}}`, 833},
		{`{"type":"message_delta","usage":{"output_tokens_details":{}}}`, 833},
		{`{"type":"message_delta","usage":{"output_tokens_details":{"thinking_tokens":0}}}`, 0},
	} {
		var event dto.ClaudeResponse
		require.NoError(t, common.UnmarshalJsonStr(tc.body, &event))
		require.True(t, FormatClaudeResponseInfo(&event, nil, state))
		require.Equal(t, tc.want, state.Usage.CompletionTokenDetails.ReasoningTokens)
		usage := buildOpenAIStyleUsageFromClaudeUsage(state.Usage)
		require.Equal(t, tc.want, usage.CompletionTokenDetails.ReasoningTokens)
	}
	require.Equal(t, 1409, state.Usage.CompletionTokens)
	require.Equal(t, 1509, state.Usage.TotalTokens)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	state.Usage.CompletionTokenDetails.ReasoningTokens = 833
	HandleStreamFinalResponse(c, &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI, ShouldIncludeUsage: true, ChannelMeta: &relaycommon.ChannelMeta{}, OriginModelName: "claude-sonnet-5-5"}, state)
	require.Contains(t, recorder.Body.String(), `"reasoning_tokens":833`)
	require.Contains(t, recorder.Body.String(), `"completion_tokens":1409`)
}
