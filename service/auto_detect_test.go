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

func TestChannelClientProbeFormatUsesStructuredSetting(t *testing.T) {
	cc := `{"key_group":"Claude Max（仅限CC）","client_exclusive":"claude_code"}`
	codex := `{"key_group":"codex","client_exclusive":"codex"}`
	labelOnly := `{"key_group":"cc"}`

	require.Equal(t, "claude-cli", ChannelClientProbeFormat(&model.Channel{Setting: &cc}))
	require.Equal(t, "codex-cli", ChannelClientProbeFormat(&model.Channel{Setting: &codex}))
	require.Empty(t, ChannelClientProbeFormat(&model.Channel{Setting: &labelOnly}))
	require.Empty(t, ChannelClientProbeFormat(nil))
}
