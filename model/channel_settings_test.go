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

func TestChannelValidateImageResolutionModelMapping(t *testing.T) {
	for _, tc := range []struct {
		setting string
		valid   bool
	}{
		{`{"image_resolution_model_mapping":{"gemini-3.1-flash-image":{"1k":"gemini-3.1-flash-image","4k":"gemini-3.1-flash-image-preview"}}}`, true},
		{`{"image_resolution_model_mapping":{"x":{"4K":"y"}}}`, false},
		{`{"image_resolution_model_mapping":{"x":{"4k":" "}}}`, false},
		{`{"image_resolution_model_mapping":{" x":{"1k":"y"}}}`, false},
	} {
		channel := &Channel{Setting: common.GetPointer(tc.setting)}
		err := channel.ValidateSettings()
		if (err == nil) != tc.valid {
			t.Fatalf("validation for %s: %v", tc.setting, err)
		}
	}
}
