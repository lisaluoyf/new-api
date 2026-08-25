package service

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestFingerprintBaseURLUsesOfficialZhipuV4Root(t *testing.T) {
	empty := ""
	custom := "https://relay.example/v1/"

	require.Equal(
		t, "https://open.bigmodel.cn/api/paas/v4",
		fingerprintBaseURL(&model.Channel{Type: constant.ChannelTypeZhipu, BaseURL: &empty}),
	)
	require.Equal(
		t, "https://relay.example/v1",
		fingerprintBaseURL(&model.Channel{Type: constant.ChannelTypeZhipu, BaseURL: &custom}),
	)
}

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
