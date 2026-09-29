package claude

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAdaptiveModelEffortMatrix(t *testing.T) {
	for _, model := range []string{"claude-opus-4-7", "claude-opus-4-8", "claude-opus-5", "claude-opus-5-5", "claude-sonnet-5", "claude-sonnet-5-5", "claude-fable-5", "claude-fable-5-1", "claude-mythos-5", "claude-mythos-5-1"} {
		for _, effort := range []string{"", "low", "medium", "high", "xhigh", "max"} {
			for _, stream := range []bool{false, true} {
				t.Run(model+"/"+effort, func(t *testing.T) {
					in := dto.GeneralOpenAIRequest{Model: model, ReasoningEffort: effort, Stream: common.GetPointer(stream), MaxTokens: common.GetPointer[uint](700), Temperature: common.GetPointer(0.0), TopP: common.GetPointer(0.5), TopK: common.GetPointer(10)}
					out, err := RequestOpenAI2ClaudeMessage(nil, in)
					require.NoError(t, err)
					raw, err := common.Marshal(out)
					require.NoError(t, err)
					if effort == "" && (model == "claude-opus-4-7" || model == "claude-opus-4-8") {
						require.Nil(t, out.Thinking)
					} else {
						require.Equal(t, "adaptive", gjson.GetBytes(raw, "thinking.type").String())
						require.Equal(t, "summarized", gjson.GetBytes(raw, "thinking.display").String())
					}
					require.False(t, gjson.GetBytes(raw, "thinking.budget_tokens").Exists())
					require.Nil(t, out.Temperature)
					require.Nil(t, out.TopP)
					require.Nil(t, out.TopK)
					require.Equal(t, effort, gjson.GetBytes(raw, "output_config.effort").String())
					require.Equal(t, int64(700), gjson.GetBytes(raw, "max_tokens").Int())
					if effort == "" {
						require.Empty(t, out.OutputConfig)
					}
				})
			}
		}
	}
}

func TestAdaptiveThinkingExplicitSettings(t *testing.T) {
	for _, tc := range []struct {
		model, body, thinking, effort, display string
		bad                                    bool
	}{
		{"claude-sonnet-5-5", `"thinking":{"type":"between_tools"}`, "between_tools", "", "", false},
		{"claude-sonnet-5-5", `"thinking":{"type":"between_tools"},"reasoning_effort":"high"`, "between_tools", "high", "", false},
		{"claude-sonnet-5-5", `"thinking":{"type":"between_tools"},"reasoning_effort":"xhigh"`, "", "", "", true},
		{"claude-sonnet-5-5", `"thinking":{"type":"between_tools"},"reasoning_effort":"max"`, "", "", "", true},
		{"claude-sonnet-5-5", `"thinking":{"type":"between_tools","display":"omitted"}`, "", "", "", true},
		{"claude-sonnet-5-5", `"thinking":{"type":"between_tools","block_binding":"x"}`, "", "", "", true},
		{"claude-sonnet-5-5", `"thinking":{"type":"between_tools"},"reasoning":{"exclude":true}`, "", "", "", true},
		{"claude-sonnet-5-5", `"thinking":{"type":"disabled"}`, "", "", "", true},
		{"claude-sonnet-5-5", `"reasoning":{"enabled":false}`, "", "", "", true},
		{"claude-sonnet-5-5", `"reasoning_effort":"none"`, "", "", "", true},
		{"claude-sonnet-5-5", `"reasoning_effort":"minimal"`, "", "", "", true},
		{"claude-sonnet-5-5", `"thinking":{"type":"enabled","budget_tokens":2048}`, "", "", "", true},
		{"claude-sonnet-5-5", `"thinking":{"type":"adaptive","budget_tokens":0}`, "", "", "", true},
		{"claude-sonnet-5-5", `"reasoning":{"max_tokens":0}`, "", "", "", true},
		{"claude-sonnet-5-5", `"thinking":null`, "", "", "", true},
		{"claude-sonnet-5-5", `"thinking":[]`, "", "", "", true},
		{"claude-sonnet-5-5", `"reasoning":{"effort":"low"},"reasoning_effort":"max"`, "adaptive", "max", "summarized", false},
		{"claude-sonnet-5-5", `"thinking":{"type":"adaptive","display":"omitted"},"reasoning_effort":"high"`, "adaptive", "high", "omitted", false},
		{"claude-sonnet-5-5", `"reasoning":{"exclude":true,"effort":"high"}`, "adaptive", "high", "omitted", false},
		{"claude-opus-5", `"thinking":{"type":"disabled"},"reasoning_effort":"high"`, "disabled", "high", "", false},
		{"claude-opus-5", `"thinking":{"type":"disabled"},"reasoning_effort":"max"`, "", "", "", true},
		{"claude-sonnet-5", `"thinking":{"type":"disabled"},"reasoning_effort":"max"`, "disabled", "max", "", false},
		{"claude-opus-4-7", `"reasoning_effort":"none"`, "disabled", "", "", false},
		{"claude-opus-4-8", `"reasoning":{"enabled":false}`, "disabled", "", "", false},
		{"claude-opus-4-8", `"thinking":{"type":"disabled","display":"omitted"}`, "", "", "", true},
		{"claude-opus-4-8", `"thinking":{"type":"disabled"},"reasoning":{"enabled":true}`, "", "", "", true},
		{"claude-sonnet-5", `"thinking":{"type":"adaptive"},"reasoning":{"enabled":false}`, "", "", "", true},
		{"claude-fable-5-1", `"thinking":{"type":"disabled"}`, "", "", "", true},
		{"claude-mythos-5", `"thinking":{"type":"between_tools"}`, "", "", "", true},
	} {
		t.Run(tc.model+tc.body, func(t *testing.T) {
			var in dto.GeneralOpenAIRequest
			require.NoError(t, common.Unmarshal([]byte(`{"model":"`+tc.model+`",`+tc.body+`}`), &in))
			out, err := RequestOpenAI2ClaudeMessage(nil, in)
			if tc.bad {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.thinking, out.Thinking.Type)
			require.Nil(t, out.Thinking.BudgetTokens)
			require.Equal(t, tc.display, out.Thinking.Display)
			require.Equal(t, tc.effort, gjson.GetBytes(out.OutputConfig, "effort").String())
		})
	}
}

func TestAdaptiveAliasesAndFutureConfiguration(t *testing.T) {
	for _, id := range []string{"claude-sonnet-5-5-high", "claude-sonnet-5-5-20260928-high", "claude-sonnet-5-5-thinking"} {
		out, err := RequestOpenAI2ClaudeMessage(nil, dto.GeneralOpenAIRequest{Model: id, MaxTokens: common.GetPointer[uint](700)})
		require.NoError(t, err)
		require.Equal(t, "adaptive", out.Thinking.Type)
		require.Equal(t, "high", gjson.GetBytes(out.OutputConfig, "effort").String())
		require.Equal(t, uint(700), *out.MaxTokens)
	}
	settings := model_setting.GetClaudeSettings()
	old := settings.ThinkingModelProfiles
	t.Cleanup(func() { settings.ThinkingModelProfiles = old })
	settings.ThinkingModelProfiles = map[string]string{"future-model": "adaptive_between_tools", "bad-config": "typo"}
	out, err := RequestOpenAI2ClaudeMessage(nil, dto.GeneralOpenAIRequest{Model: "future-model", ReasoningEffort: "xhigh"})
	require.NoError(t, err)
	require.Equal(t, "adaptive", out.Thinking.Type)
	require.Equal(t, "xhigh", gjson.GetBytes(out.OutputConfig, "effort").String())
	_, err = RequestOpenAI2ClaudeMessage(nil, dto.GeneralOpenAIRequest{Model: "bad-config"})
	require.ErrorContains(t, err, "unknown Claude thinking profile")
}
