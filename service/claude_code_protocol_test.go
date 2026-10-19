package service

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Reproduce OpenAI calls carrying Claude CLI headers. The protocol boundary
// must hold on initial selection, fallback and explicit/affinity validation.
func TestClaudeCodeProtocolBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, agent, beta string
		allowed                         bool
	}{
		{"native_cli", "POST", "/v1/messages", "claude-cli/2.0.0", "", true},
		{"native_beta", "POST", "/v1/messages", "custom-client", "claude-code-20250219", true},
		{"native_query", "POST", "/v1/messages?beta=true", "Claude-CLI/2.0.0", "", true},
		{"native_generic", "POST", "/v1/messages", "python-httpx", "", false},
		{"chat_cli", "POST", "/v1/chat/completions", "claude-cli/2.0.0", "", false},
		{"chat_cli_beta", "POST", "/v1/chat/completions", "claude-cli/2.0.0", "claude-code-20250219", false},
		{"responses_cli", "POST", "/v1/responses", "claude-cli/2.0.0", "claude-code-20250219", false},
		{"playground_cli", "POST", "/pg/chat/completions", "claude-cli/2.0.0", "", false},
		{"get_messages", "GET", "/v1/messages", "claude-cli/2.0.0", "", false},
		{"count_tokens", "POST", "/v1/messages/count_tokens", "claude-cli/2.0.0", "claude-code-20250219", false},
		{"path_contains_messages", "POST", "/proxy/v1/messages", "claude-cli/2.0.0", "claude-code-20250219", false},
		{"query_contains_messages", "POST", "/v1/chat/completions?route=/v1/messages", "claude-cli/2.0.0", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, name := range []string{"claude-sonnet-5-5", "custom-alias"} {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"model":"`+name+`","messages":[]}`))
				c.Request.Header.Set("User-Agent", tc.agent)
				c.Request.Header.Set("anthropic-beta", tc.beta)
				require.Equal(t, tc.allowed, DetectClaudeCodeClient(c))
				exclusive := &model.Channel{Id: 15, Setting: strPtr(`{"client_exclusive":"claude_code"}`)}
				generic := &model.Channel{Id: 221, Setting: strPtr(`{}`)}
				for attempt := 0; attempt < 2; attempt++ {
					filter := ChannelPickFilter(c, name)
					require.Equal(t, tc.allowed, filter(exclusive), "model=%s attempt=%d", name, attempt)
					require.True(t, filter(generic))
				}
				require.Equal(t, tc.allowed, ValidateChannelClientPolicy(c, exclusive, name) == nil)
				require.NoError(t, ValidateChannelClientPolicy(c, generic, name))
			}
		})
	}
}

func TestClaudeCodeMissingRequestContext(t *testing.T) {
	require.False(t, DetectClaudeCodeClient(nil))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.False(t, DetectClaudeCodeClient(c))
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	c.Request.Header.Set("User-Agent", "claude-cli/2.0.0")
	c.Request.URL = nil
	require.False(t, DetectClaudeCodeClient(c))
}
