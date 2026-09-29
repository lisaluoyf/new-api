package common

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMaskSensitiveInfoPreservesThinkingFieldPaths(t *testing.T) {
	for _, s := range []string{"thinking.type=adaptive", "thinking.type.enabled", "thinking.type.disabled", "thinking.type.adaptive", "thinking.display=summarized", "thinking.type=between_tools", "thinking.budget_tokens", "reasoning.max_tokens", "reasoning.enabled", "reasoning.exclude", "output_config.effort"} {
		require.Equal(t, s, MaskSensitiveInfo(s))
	}
	for _, s := range []string{"https://thinking.type/secret?key=secret", "thinking.type.example.com", "https://reasoning.enabled/secret", "reasoning.exclude.example.com", "private.example.com", "192.168.1.2"} {
		require.NotEqual(t, s, MaskSensitiveInfo(s))
	}
}
