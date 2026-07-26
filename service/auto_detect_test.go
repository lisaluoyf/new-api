package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestExtractAPIFormatHonorsClientExclusive(t *testing.T) {
	cc := `{"api_format":"openai-compatible","client_exclusive":"claude_code"}`
	codex := `{"api_format":"openai-compatible","client_exclusive":"codex"}`
	plain := `{"api_format":"anthropic"}`

	require.Equal(t, "claude-cli", extractAPIFormat(&cc))
	require.Equal(t, "codex-cli", extractAPIFormat(&codex))
	require.Equal(t, "anthropic", extractAPIFormat(&plain))
	require.Equal(t, "openai-compatible", extractAPIFormat(nil))
}

func TestChannelRequiresClaudeCodeProbeUsesStructuredSetting(t *testing.T) {
	cc := `{"key_group":"Claude Max（仅限CC）","client_exclusive":"claude_code"}`
	labelOnly := `{"key_group":"cc"}`

	require.True(t, ChannelRequiresClaudeCodeProbe(&model.Channel{Setting: &cc}))
	require.False(t, ChannelRequiresClaudeCodeProbe(&model.Channel{Setting: &labelOnly}))
	require.False(t, ChannelRequiresClaudeCodeProbe(nil))
}
