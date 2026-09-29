package model_setting

import (
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/reasoning"
)

// Verified against Anthropic's thinking capability table on 2026-09-29.
// https://platform.claude.com/docs/en/build-with-claude/thinking
var builtinClaudeThinkingProfiles = map[string]string{
	"claude-opus-4-7":   "adaptive_opt_in",
	"claude-opus-4-8":   "adaptive_opt_in",
	"claude-opus-5":     "adaptive_default_on_high",
	"claude-opus-5-5":   "adaptive_required",
	"claude-sonnet-5":   "adaptive_default_on",
	"claude-sonnet-5-5": "adaptive_between_tools",
	"claude-fable-5":    "adaptive_required",
	"claude-fable-5-1":  "adaptive_required",
	"claude-mythos-5":   "adaptive_required",
	"claude-mythos-5-1": "adaptive_required",
}

// ClaudeThinkingProfile supports explicit IDs, effort aliases and dated
// snapshots. It deliberately does not prefix-match arbitrary future releases.
// The options API can register future models through
// claude.thinking_model_profiles without rebuilding the gateway.
func ClaudeThinkingProfile(model string) string {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	return lookupClaudeThinkingProfile(model, claudeSettings.ThinkingModelProfiles)
}

func lookupClaudeThinkingProfile(model string, overrides map[string]string) string {
	lookup := func(id string) (string, bool) {
		if profile, ok := overrides[id]; ok {
			return profile, true
		}
		profile, ok := builtinClaudeThinkingProfiles[id]
		return profile, ok
	}
	if profile, ok := lookup(model); ok {
		return profile
	}
	model, _, _ = reasoning.TrimEffortSuffix(model)
	model = strings.TrimSuffix(model, "-thinking")
	if profile, ok := lookup(model); ok {
		return profile
	}
	if i := strings.LastIndexByte(model, '-'); i >= 0 {
		if _, err := time.Parse("20060102", model[i+1:]); err == nil {
			profile, _ := lookup(model[:i])
			return profile
		}
	}
	return ""
}
