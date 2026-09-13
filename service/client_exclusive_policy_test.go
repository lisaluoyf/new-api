package service

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestExtractClientExclusive(t *testing.T) {
	require.Equal(t, ClientExclusiveCodex, ExtractClientExclusive(strPtr(`{"client_exclusive":"codex"}`)))
	require.Equal(t, ClientExclusiveClaudeCode, ExtractClientExclusive(strPtr(`{"client_exclusive":"claude_code"}`)))
	require.Equal(t, ClientExclusiveNone, ExtractClientExclusive(strPtr(`{"client_exclusive":""}`)))
	require.Equal(t, ClientExclusiveNone, ExtractClientExclusive(strPtr(`{"key_group":"Codex 正价"}`)))
	require.Equal(t, ClientExclusiveNone, ExtractClientExclusive(strPtr(`{"key_group":"Codex Pro（外接版）","client_exclusive":""}`)))
	require.Equal(t, ClientExclusiveNone, ExtractClientExclusive(strPtr(`{"key_group":"cc"}`)))
	require.Equal(t, ClientExclusiveNone, ExtractClientExclusive(nil))
}

func TestChannelMatchesClientPolicy_codexOneWay(t *testing.T) {
	codexSetting := strPtr(`{"client_exclusive":"codex"}`)
	genericSetting := strPtr(`{"key_group":"default"}`)

	require.True(t, ChannelMatchesClientPolicy(codexSetting, ClientTypeCodex, "gpt-5.4"))
	require.False(t, ChannelMatchesClientPolicy(codexSetting, ClientTypeGeneric, "gpt-5.4"))
	require.True(t, ChannelMatchesClientPolicy(genericSetting, ClientTypeGeneric, "gpt-5.4"))
	require.True(t, ChannelMatchesClientPolicy(genericSetting, ClientTypeCodex, "gpt-5.4"))
}

func TestChannelMatchesClientPolicy_claudeCodeOneWay(t *testing.T) {
	ccSetting := strPtr(`{"client_exclusive":"claude_code"}`)
	genericSetting := strPtr(`{"key_group":"default"}`)

	require.True(t, ChannelMatchesClientPolicy(ccSetting, ClientTypeClaudeCode, "claude-sonnet-4-6"))
	require.False(t, ChannelMatchesClientPolicy(ccSetting, ClientTypeGeneric, "claude-sonnet-4-6"))
	require.True(t, ChannelMatchesClientPolicy(genericSetting, ClientTypeClaudeCode, "claude-sonnet-4-6"))
	require.True(t, ChannelMatchesClientPolicy(genericSetting, ClientTypeGeneric, "claude-sonnet-4-6"))
}

func TestChannelMatchesClientPolicy_matrix(t *testing.T) {
	settings := map[string]*string{
		"generic": strPtr(`{"key_group":"default"}`),
		"codex":   strPtr(`{"client_exclusive":"codex"}`),
		"cc":      strPtr(`{"client_exclusive":"claude_code"}`),
	}
	clients := []ClientType{ClientTypeGeneric, ClientTypeCodex, ClientTypeClaudeCode}
	modelName := "claude-sonnet-4-6"

	want := map[ClientType]map[string]bool{
		ClientTypeGeneric:    {"generic": true, "codex": false, "cc": false},
		ClientTypeCodex:      {"generic": true, "codex": true, "cc": false},
		ClientTypeClaudeCode: {"generic": true, "codex": false, "cc": true},
	}

	for _, client := range clients {
		for chName, setting := range settings {
			got := ChannelMatchesClientPolicy(setting, client, modelName)
			require.Equal(t, want[client][chName], got, "client=%s channel=%s", client, chName)
		}
	}
}

func TestDetectClaudeCodeClient_userAgent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"claude-sonnet-4-6"}`))
	c.Request.Header.Set("User-Agent", "claude-cli/1.0.0")
	require.True(t, DetectClaudeCodeClient(c))
}

func TestDetectClaudeCodeClient_anthropicBeta(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"claude-sonnet-4-6"}`))
	c.Request.Header.Set("anthropic-beta", "claude-code-20250219")
	require.True(t, DetectClaudeCodeClient(c))
}

func TestDetectClaudeCodeClient_openAICompatNotCC(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"claude-sonnet-4-6"}`))
	c.Request.Header.Set("anthropic-beta", "claude-code-20250219")
	require.False(t, DetectClaudeCodeClient(c))
}

func TestDetectClientType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"claude-fable-5"}`))
	c.Request.Header.Set("User-Agent", "claude-cli/2.0.0")
	require.Equal(t, ClientTypeClaudeCode, DetectClientType(c, "claude-fable-5"))
	require.Equal(t, ClientTypeClaudeCode, DetectClientType(c, "gpt-5.4"))
}

func TestValidateChannelClientPolicy_ccExclusive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"claude-fable-5"}`))
	c.Request.Header.Set("User-Agent", "curl/8.0")

	ch := &model.Channel{Setting: strPtr(`{"client_exclusive":"claude_code"}`)}
	err := ValidateChannelClientPolicy(c, ch, "claude-fable-5")
	require.ErrorIs(t, err, ErrNonClaudeCodeClaudeChannel)
}

// Exercise the same filter used on initial selection and fallback, plus the
// validator used for explicit channels and affinity reuse, across model aliases.
func TestClientExclusiveAllModelsRouting(t *testing.T) {
	for _, name := range []string{"gpt-5.4", "gpt-5.6-luna", "gpt-6-astra", "claude-fable-5", "anthropic/claude-fable-5", "custom-alias"} {
		for _, client := range []ClientType{ClientTypeGeneric, ClientTypeCodex, ClientTypeClaudeCode} {
			t.Run(name+"/"+string(client), func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"`+name+`"}`))
				if client == ClientTypeCodex {
					c.Request.Header.Set("Originator", "codex_cli_rs")
				}
				if client == ClientTypeClaudeCode {
					c.Request.Header.Set("User-Agent", "claude-cli/2.0.0")
				}
				require.Equal(t, client, DetectClientType(c, name))
				require.True(t, RequiresClientExclusivePolicy(name))
				for _, exclusive := range []string{"", "codex", "claude_code"} {
					ch := &model.Channel{Setting: strPtr(`{"client_exclusive":"` + exclusive + `"}`)}
					want := exclusive == "" || exclusive == string(client)
					for attempt := 0; attempt < 2; attempt++ {
						require.Equal(t, want, ChannelPickFilter(c, name)(ch), "exclusive=%s attempt=%d", exclusive, attempt)
					}
					require.Equal(t, want, ValidateChannelClientPolicy(c, ch, name) == nil, "exclusive=%s", exclusive)
				}
				other := map[string]interface{}{}
				AppendClientExclusiveLogInfo(c, name, other)
				wantLog := string(client)
				if client == ClientTypeGeneric {
					wantLog = "openai_compatible"
				}
				require.Equal(t, wantLog, other["client_type"])
			})
		}
	}
}
