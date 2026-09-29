package model_setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClaudeThinkingProfileMatching(t *testing.T) {
	for _, tc := range []struct{ model, profile string }{
		{"claude-sonnet-5-5", "adaptive_between_tools"},
		{"claude-sonnet-5-5-high", "adaptive_between_tools"},
		{"claude-sonnet-5-5-20260928-max", "adaptive_between_tools"},
		{"claude-opus-5-5-thinking", "adaptive_required"},
		{"claude-opus-5", "adaptive_default_on_high"},
		{"claude-sonnet-5", "adaptive_default_on"},
		{"claude-fable-5-1", "adaptive_required"},
		{"claude-mythos-5-1", "adaptive_required"},
		{"claude-opus-4-8", "adaptive_opt_in"},
		{"claude-opus-5-50", ""},
		{"claude-sonnet-5-50", ""},
		{"claude-opus-5-5-custom", ""},
		{"claude-sonnet-5-5-20269999", ""},
		{"claude-sonnet-6", ""},
		{"claude-haiku-4-5", ""},
	} {
		t.Run(tc.model, func(t *testing.T) {
			require.Equal(t, tc.profile, lookupClaudeThinkingProfile(tc.model, nil))
		})
	}
}

func TestClaudeThinkingProfileOverrides(t *testing.T) {
	overrides := map[string]string{"claude-future": "adaptive_required", "claude-sonnet-5-5": "legacy"}
	require.Equal(t, "adaptive_required", lookupClaudeThinkingProfile("claude-future-high", overrides))
	require.Equal(t, "adaptive_required", lookupClaudeThinkingProfile("claude-future-20260929", overrides))
	require.Equal(t, "legacy", lookupClaudeThinkingProfile("claude-sonnet-5-5-low", overrides))
	require.Equal(t, "adaptive_required", lookupClaudeThinkingProfile("claude-opus-5-5", overrides))
	require.Empty(t, lookupClaudeThinkingProfile("claude-future-2", overrides))
}
