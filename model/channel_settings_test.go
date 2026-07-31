package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
)

func TestChannelValidateSettingsRejectsConflictingThinkTransforms(t *testing.T) {
	setting := `{"thinking_to_content":true,"strip_prefix_think_block":true}`
	channel := &Channel{Setting: common.GetPointer(setting)}
	if err := channel.ValidateSettings(); err == nil {
		t.Fatal("expected conflicting think transforms to be rejected")
	}
}

func TestChannelValidateSettingsAcceptsScopedPrefixThinkFilter(t *testing.T) {
	setting := `{"strip_prefix_think_block":true,"strip_prefix_think_models":["grok-4.5"]}`
	channel := &Channel{Setting: common.GetPointer(setting)}
	if err := channel.ValidateSettings(); err != nil {
		t.Fatalf("expected valid prefix think filter settings: %v", err)
	}
}

func TestChannelValidateSettingsAcceptsCacheExclusiveModels(t *testing.T) {
	setting := `{"cache_exclusive_models":["deepseek-v4-flash","glm-5.2"]}`
	channel := &Channel{Setting: common.GetPointer(setting)}
	if err := channel.ValidateSettings(); err != nil {
		t.Fatalf("expected valid cache-exclusive model settings: %v", err)
	}
}

func TestChannelValidateSettingsRejectsEmptyCacheExclusiveModel(t *testing.T) {
	setting := `{"cache_exclusive_models":["deepseek-v4-flash"," "]}`
	channel := &Channel{Setting: common.GetPointer(setting)}
	if err := channel.ValidateSettings(); err == nil {
		t.Fatal("expected empty cache-exclusive model name to be rejected")
	}
}
