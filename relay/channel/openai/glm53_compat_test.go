package openai

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/dto"
)

func TestNormalizeGLM53Reasoning(t *testing.T) {
	tests := []struct {
		name             string
		model            string
		reasoningEffort  string
		thinking         json.RawMessage
		wantEffort       string
		wantThinkingType string
		wantChanged      bool
	}{
		{name: "none becomes low", model: "glm-5.3", reasoningEffort: "none", wantEffort: "low", wantThinkingType: "enabled", wantChanged: true},
		{name: "off becomes low", model: "glm-5.3", reasoningEffort: "off", wantEffort: "low", wantThinkingType: "enabled", wantChanged: true},
		{name: "disabled becomes low", model: "glm-5.3", reasoningEffort: "disabled", wantEffort: "low", wantThinkingType: "enabled", wantChanged: true},
		{name: "minimal becomes low", model: "glm-5.3", reasoningEffort: "minimal", wantEffort: "low", wantThinkingType: "enabled", wantChanged: true},
		{name: "disabled thinking becomes enabled low", model: "glm-5.3", thinking: json.RawMessage(`{"type":"disabled"}`), wantEffort: "low", wantThinkingType: "enabled", wantChanged: true},
		{name: "low stays low", model: "glm-5.3", reasoningEffort: "low", wantEffort: "low", wantThinkingType: "enabled", wantChanged: true},
		{name: "high stays high", model: "glm-5.3", reasoningEffort: "high", wantEffort: "high", wantThinkingType: "enabled", wantChanged: true},
		{name: "max stays max", model: "glm-5.3", reasoningEffort: "max", wantEffort: "max", wantThinkingType: "enabled", wantChanged: true},
		{name: "missing reasoning uses legal compatibility default", model: "glm-5.3", wantEffort: "low", wantThinkingType: "enabled", wantChanged: true},
		{name: "glm-5.2 remains unchanged", model: "glm-5.2", thinking: json.RawMessage(`{"type":"disabled"}`), wantThinkingType: "disabled", wantChanged: false},
		{name: "OpenAI model remains unchanged", model: "gpt-5.4", reasoningEffort: "none", wantEffort: "none", wantChanged: false},
		{name: "Claude model remains unchanged", model: "claude-opus-4-6", thinking: json.RawMessage(`{"type":"disabled"}`), wantThinkingType: "disabled", wantChanged: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := &dto.GeneralOpenAIRequest{
				ReasoningEffort: test.reasoningEffort,
				THINKING:        test.thinking,
			}
			result := normalizeGLM53Reasoning(test.model, request)
			if result.Changed != test.wantChanged {
				t.Fatalf("Changed = %v, want %v", result.Changed, test.wantChanged)
			}
			if request.ReasoningEffort != test.wantEffort {
				t.Fatalf("ReasoningEffort = %q, want %q", request.ReasoningEffort, test.wantEffort)
			}
			if got := thinkingType(t, request.THINKING); got != test.wantThinkingType {
				t.Fatalf("thinking.type = %q, want %q", got, test.wantThinkingType)
			}
			if test.model == "glm-5.3" && (request.ReasoningEffort == "none" || request.ReasoningEffort == "disabled") {
				t.Fatalf("glm-5.3 retained disabled reasoning: %q", request.ReasoningEffort)
			}
		})
	}
}

func TestNormalizeGLM53ReasoningPreservesJanitorSampling(t *testing.T) {
	temperature := 1.0
	topP := 0.45
	topK := 45
	frequencyPenalty := 1.45
	request := &dto.GeneralOpenAIRequest{
		Temperature:      &temperature,
		TopP:             &topP,
		TopK:             &topK,
		FrequencyPenalty: &frequencyPenalty,
	}

	normalizeGLM53Reasoning("glm-5.3", request)
	if request.Temperature == nil || *request.Temperature != temperature ||
		request.TopP == nil || *request.TopP != topP ||
		request.TopK == nil || *request.TopK != topK ||
		request.FrequencyPenalty == nil || *request.FrequencyPenalty != frequencyPenalty {
		t.Fatal("Janitor sampling parameters changed during GLM-5.3 compatibility normalization")
	}
	if request.ReasoningEffort != "low" || thinkingType(t, request.THINKING) != "enabled" {
		t.Fatalf("compatibility fields = effort %q thinking %q", request.ReasoningEffort, thinkingType(t, request.THINKING))
	}
}

func TestNormalizeGLM53ReasoningLegalEffortWinsConflicts(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		ReasoningEffort:    "high",
		THINKING:           json.RawMessage(`{"type":"disabled","effort":"max"}`),
		Reasoning:          json.RawMessage(`{"enabled":false,"effort":"low"}`),
		EnableThinking:     json.RawMessage(`false`),
		Think:              json.RawMessage(`false`),
		ChatTemplateKwargs: json.RawMessage(`{"enable_thinking":false,"unrelated":"preserved"}`),
		ExtraBody:          json.RawMessage(`{"thinking":{"type":"disabled"},"other":7}`),
	}

	result := normalizeGLM53Reasoning("glm-5.3", request)
	if result.Effort != "high" || request.ReasoningEffort != "high" {
		t.Fatalf("legal top-level effort did not win: result=%+v request=%q", result, request.ReasoningEffort)
	}
	if got := thinkingType(t, request.THINKING); got != "enabled" {
		t.Fatalf("thinking.type = %q, want enabled", got)
	}
	if len(request.Reasoning) != 0 || len(request.EnableThinking) != 0 || len(request.Think) != 0 {
		t.Fatal("conflicting top-level reasoning fields were not removed")
	}
	assertJSONObject(t, request.ChatTemplateKwargs, map[string]any{"unrelated": "preserved"})
	assertJSONObject(t, request.ExtraBody, map[string]any{"other": float64(7)})
}

func TestNormalizeGLM53ReasoningAlternateLegalPriority(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Reasoning: json.RawMessage(`{"effort":"max"}`),
		THINKING:  json.RawMessage(`{"type":"disabled","effort":"high"}`),
	}
	result := normalizeGLM53Reasoning("glm-5.3", request)
	if result.Effort != "max" || result.Source != "reasoning.effort" {
		t.Fatalf("result = %+v, want reasoning.effort max", result)
	}
}

func thinkingType(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	if len(raw) == 0 {
		return ""
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("invalid thinking JSON: %v", err)
	}
	value, _ := object["type"].(string)
	return value
}

func assertJSONObject(t *testing.T, raw json.RawMessage, want map[string]any) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("object = %#v, want %#v", got, want)
	}
	for key, wantValue := range want {
		if got[key] != wantValue {
			t.Fatalf("object[%q] = %#v, want %#v", key, got[key], wantValue)
		}
	}
}
