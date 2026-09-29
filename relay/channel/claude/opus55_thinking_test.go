package claude

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"testing"
)

func TestOpus55EffortAndDisplay(t *testing.T) {
	for _, effort := range []string{"", "low", "medium", "high", "xhigh", "max"} {
		for _, stream := range []bool{false, true} {
			t.Run(effort, func(t *testing.T) {
				in := dto.GeneralOpenAIRequest{Model: "claude-opus-5-5", ReasoningEffort: effort, Stream: common.GetPointer(stream)}
				out, err := RequestOpenAI2ClaudeMessage(nil, in)
				require.NoError(t, err)
				raw, err := common.Marshal(out)
				require.NoError(t, err)
				require.Equal(t, "adaptive", gjson.GetBytes(raw, "thinking.type").String())
				require.Equal(t, "summarized", gjson.GetBytes(raw, "thinking.display").String())
				require.False(t, gjson.GetBytes(raw, "thinking.budget_tokens").Exists())
				require.Equal(t, effort, gjson.GetBytes(raw, "output_config.effort").String())
				if effort == "" {
					require.False(t, gjson.GetBytes(raw, "output_config").Exists())
				}
			})
		}
	}
}

func TestOpus55ExplicitReasoning(t *testing.T) {
	for _, tc := range []struct {
		body, effort, display string
		bad                   bool
	}{
		{`"reasoning_effort":"high","thinking":{"type":"adaptive","display":"omitted"}`, "high", "omitted", false},
		{`"reasoning":{"effort":"max","exclude":true}`, "max", "omitted", false},
		{`"reasoning":{"effort":"low"},"reasoning_effort":"xhigh"`, "xhigh", "summarized", false},
		{`"reasoning_effort":"none"`, "", "", true},
		{`"reasoning_effort":"minimal"`, "", "", true},
		{`"thinking":{"type":"disabled"}`, "", "", true},
		{`"thinking":{"type":"enabled","budget_tokens":2048}`, "", "", true},
		{`"reasoning":{"max_tokens":2048}`, "", "", true},
		{`"reasoning":{"enabled":false}`, "", "", true},
	} {
		t.Run(tc.body, func(t *testing.T) {
			var in dto.GeneralOpenAIRequest
			require.NoError(t, common.Unmarshal([]byte(`{"model":"claude-opus-5-5",`+tc.body+`}`), &in))
			out, err := RequestOpenAI2ClaudeMessage(nil, in)
			if tc.bad {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.display, out.Thinking.Display)
			require.Equal(t, tc.effort, gjson.GetBytes(out.OutputConfig, "effort").String())
		})
	}
}

func TestOpus55SuffixAndLegacyModel(t *testing.T) {
	out, err := RequestOpenAI2ClaudeMessage(nil, dto.GeneralOpenAIRequest{Model: "claude-opus-5-5-xhigh"})
	require.NoError(t, err)
	require.Equal(t, "claude-opus-5-5", out.Model)
	require.Equal(t, "xhigh", gjson.GetBytes(out.OutputConfig, "effort").String())
	for _, model := range []string{"claude-opus-5", "claude-sonnet-4-5", "claude-opus-5-50"} {
		out, err := RequestOpenAI2ClaudeMessage(nil, dto.GeneralOpenAIRequest{Model: model, ReasoningEffort: "low"})
		require.NoError(t, err)
		require.Equal(t, "enabled", out.Thinking.Type)
		require.Equal(t, 1280, *out.Thinking.BudgetTokens)
	}
}

func TestClaudeResponsePreservesAllBlocks(t *testing.T) {
	var in dto.ClaudeResponse
	require.NoError(t, common.Unmarshal([]byte(`{"content":[{"type":"thinking","thinking":"first "},{"type":"text","text":"hello "},{"type":"thinking","thinking":"second"},{"type":"thinking","thinking":""},{"type":"text","text":"world"}],"stop_reason":"end_turn"}`), &in))
	out := ResponseClaude2OpenAI(&in)
	raw, err := common.Marshal(out)
	require.NoError(t, err)
	require.Equal(t, "first second", gjson.GetBytes(raw, "choices.0.message.reasoning_content").String())
	require.Equal(t, "hello world", gjson.GetBytes(raw, "choices.0.message.content").String())
}

func TestNativeThinkingDisplayPreserved(t *testing.T) {
	for _, display := range []string{"summarized", "omitted", ""} {
		in := &dto.ClaudeRequest{Model: "claude-opus-5-5", Thinking: &dto.Thinking{Type: "adaptive", Display: display}}
		out, err := (&Adaptor{}).ConvertClaudeRequest(nil, nil, in)
		require.NoError(t, err)
		raw, err := common.Marshal(out)
		require.NoError(t, err)
		require.Equal(t, display, gjson.GetBytes(raw, "thinking.display").String())
	}
}

func TestThinkingStreamStartAndDelta(t *testing.T) {
	for _, raw := range []string{
		`{"type":"content_block_start","content_block":{"type":"thinking","thinking":"summary"}}`,
		`{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"summary"}}`,
	} {
		var in dto.ClaudeResponse
		require.NoError(t, common.Unmarshal([]byte(raw), &in))
		out := StreamResponseClaude2OpenAI(&in)
		require.NotNil(t, out)
		require.Equal(t, "summary", *out.Choices[0].Delta.ReasoningContent)
	}
}
