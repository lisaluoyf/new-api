package gemini

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Exercise the actual conversion and wire JSON, including the production error
// wrapper: invalid client parameters must remain non-retryable HTTP 400 errors.
func TestGeminiThinkingLevelCompatibility(t *testing.T) {
	for _, model := range []string{"gemini-4-argon", "gemini-3.8-flash"} {
		for _, tc := range []struct {
			name, fields, level string
			invalid, thoughts   bool
		}{
			{name: "default"},
			{name: "low", fields: `"reasoning_effort":"low"`, level: "low"},
			{name: "medium", fields: `"reasoning_effort":"medium"`, level: "medium"},
			{name: "high", fields: `"reasoning_effort":"high"`, level: "high"},
			{name: "minimal", fields: `"reasoning_effort":"minimal"`, level: "low", invalid: model == "gemini-3.8-flash"},
			{name: "invalid effort", fields: `"reasoning_effort":"diagnostic_invalid"`, invalid: true},
			{name: "none", fields: `"reasoning_effort":"none"`, invalid: true},
			{name: "max", fields: `"reasoning_effort":"max"`, invalid: true},
			{name: "xhigh", fields: `"reasoning_effort":"xhigh"`, invalid: true},
			{name: "native high", fields: `"extra_body":{"google":{"thinking_config":{"thinking_level":"high"}}}`, level: "high"},
			{name: "native invalid", fields: `"extra_body":{"google":{"thinking_config":{"thinking_level":"diagnostic_invalid"}}}`, invalid: true},
			{name: "native minimal", fields: `"extra_body":{"google":{"thinking_config":{"thinking_level":"minimal"}}}`, invalid: true},
			{name: "native empty", fields: `"extra_body":{"google":{"thinking_config":{"thinking_level":""}}}`, invalid: true},
			{name: "native null", fields: `"extra_body":{"google":{"thinking_config":{"thinking_level":null}}}`, invalid: true},
			{name: "native number", fields: `"extra_body":{"google":{"thinking_config":{"thinking_level":3}}}`, invalid: true},
			{name: "same level conflict", fields: `"reasoning_effort":"high","extra_body":{"google":{"thinking_config":{"thinking_level":"high"}}}`, invalid: true},
			{name: "budget conflict", fields: `"reasoning_effort":"high","extra_body":{"google":{"thinking_config":{"thinking_budget":0}}}`, invalid: true},
			{name: "thoughts with effort", fields: `"reasoning_effort":"high","extra_body":{"google":{"thinking_config":{"include_thoughts":true}}}`, level: "high", thoughts: true},
			{name: "false thoughts with effort", fields: `"reasoning_effort":"low","extra_body":{"google":{"thinking_config":{"include_thoughts":false}}}`, level: "low"},
			{name: "thoughts only", fields: `"extra_body":{"google":{"thinking_config":{"include_thoughts":true}}}`, thoughts: true},
			{name: "malformed config", fields: `"extra_body":{"google":{"thinking_config":"high"}}`, invalid: true},
			{name: "camel level", fields: `"extra_body":{"google":{"thinking_config":{"thinkingLevel":"high"}}}`, invalid: true},
		} {
			t.Run(model+"/"+tc.name, func(t *testing.T) {
				body := `{"model":"` + model + `","messages":[{"role":"user","content":"Reply OK."}]`
				if tc.fields != "" {
					body += "," + tc.fields
				}
				body += "}"
				var request dto.GeneralOpenAIRequest
				require.NoError(t, common.Unmarshal([]byte(body), &request))
				info := &relaycommon.RelayInfo{OriginModelName: model, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: model}}
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
				converted, err := (&Adaptor{}).ConvertOpenAIRequest(c, info, &request)
				if tc.invalid {
					require.Error(t, err)
					wrapped := types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
					require.Equal(t, http.StatusBadRequest, wrapped.StatusCode)
					require.True(t, types.IsSkipRetryError(wrapped))
					return
				}
				require.NoError(t, err)
				wire, err := common.Marshal(converted)
				require.NoError(t, err)
				var payload map[string]any
				require.NoError(t, common.Unmarshal(wire, &payload))
				require.NotContains(t, payload, "reasoning_effort")
				config := converted.(*dto.GeminiChatRequest).GenerationConfig.ThinkingConfig
				if tc.fields == "" {
					require.Nil(t, config, "leave upstream default untouched")
				} else {
					require.NotNil(t, config)
					require.Equal(t, tc.level, config.ThinkingLevel)
					require.Equal(t, tc.thoughts, config.IncludeThoughts)
					require.Nil(t, config.ThinkingBudget, "levels must not become token budgets")
					if tc.level != "" {
						generation := payload["generationConfig"].(map[string]any)
						require.Equal(t, tc.level, generation["thinkingConfig"].(map[string]any)["thinkingLevel"])
					}
				}
			})
		}
	}
}

func TestGeminiThinkingLevelPreservesLegacyAdapter(t *testing.T) {
	settings := model_setting.GetGeminiSettings()
	old := settings.ThinkingAdapterEnabled
	settings.ThinkingAdapterEnabled = true
	t.Cleanup(func() { settings.ThinkingAdapterEnabled = old })
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-2.5-flash-thinking"}}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	result, err := CovertOpenAI2Gemini(c, dto.GeneralOpenAIRequest{ReasoningEffort: "high"}, info)
	require.NoError(t, err)
	require.Equal(t, flash25MaxBudget*80/100, *result.GenerationConfig.ThinkingConfig.ThinkingBudget)
}

func TestGeminiThinkingBudgetCompatibility(t *testing.T) {
	for _, tc := range []struct {
		fields  string
		invalid bool
	}{
		{`{"thinking_budget":1024}`, false},
		{`{"thinking_budget":-1}`, false},
		{`{"thinking_budget":0}`, false},
		{`{"thinking_budget":1.5}`, true},
		{`{"thinking_budget":1024,"thinking_level":"high"}`, true},
	} {
		var request dto.GeneralOpenAIRequest
		require.NoError(t, common.Unmarshal([]byte(`{"extra_body":{"google":{"thinking_config":`+tc.fields+`}}}`), &request))
		config, handled, err := normalizeThinkingLevel(request, "gemini-4-argon")
		require.True(t, handled)
		if tc.invalid {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
			require.NotNil(t, config.ThinkingBudget)
		}
	}
}
