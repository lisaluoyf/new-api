package common

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
)

func TestIsImageGenerationModel_gptImage2(t *testing.T) {
	cases := map[string]bool{
		"gpt-image-2":                    true,
		"gpt-image-2-official":           true,
		"gpt-image-1":                    true,
		"gpt-image-1-mini":               true,
		"gemini-3.1-flash-image-preview": true,
		"gemini-2.5-flash-image":         true,
		"gemini-3-pro-image":             true,
		"claude-sonnet-4-6":              false,
		"gpt-5.4":                        false,
	}
	for model, want := range cases {
		if got := IsImageGenerationModel(model); got != want {
			t.Fatalf("IsImageGenerationModel(%q) = %v, want %v", model, got, want)
		}
	}
}

func TestUsesAsyncImageTaskUpstream(t *testing.T) {
	if !UsesAsyncImageTaskUpstream("gpt-image-2") {
		t.Fatal("expected gpt-image-2 to use async upstream")
	}
	if !UsesAsyncImageTaskUpstream("gpt-image-2-official") {
		t.Fatal("expected gpt-image-2-official to use async upstream")
	}
	if UsesAsyncImageTaskUpstream("gpt-image-1") {
		t.Fatal("gpt-image-1 should not use gpt-image-2 async upstream")
	}
	if !UsesAsyncImageTaskUpstream("gemini-3.1-flash-image-preview") {
		t.Fatal("expected gemini-3.1-flash-image-preview to use async upstream")
	}
	if !UsesAsyncImageTaskUpstream("gemini-3-pro-image") {
		t.Fatal("expected gemini-3-pro-image to use async upstream")
	}
	if UsesAsyncImageTaskUpstream("gemini-3.1-flash-lite") {
		t.Fatal("text flash-lite models should not use image async upstream")
	}
}

func TestIsVideoGenerationModel(t *testing.T) {
	cases := map[string]bool{
		"minimax-h3":                 true,
		"MiniMax-H3":                 true,
		"sora-2-pro":                 true,
		"kling-v3-omni":              true,
		"doubao-seedance-2.0":        true,
		"doubao-seedance-2-0-260128": true,
		"grok-imagine-video-1.5":     true,
		"grok-1.5-video-10s":         true,
		"veo-3.1-generate-preview":   true,
		"wan2.5-i2v-preview":         true,
		"gemini-3.1-flash-image":     false,
		"grok-4.5":                   false,
		"minimax-text-01":            false,
	}
	for model, want := range cases {
		if got := IsVideoGenerationModel(model); got != want {
			t.Fatalf("IsVideoGenerationModel(%q) = %v, want %v", model, got, want)
		}
	}
}

func TestVideoModelsExposeOpenAIVideoEndpointMetadata(t *testing.T) {
	for _, modelName := range []string{
		"minimax-h3",
		"sora-2",
		"kling-v3-omni",
		"doubao-seedance-2.0",
		"grok-imagine-video-1.5",
	} {
		endpoints := GetEndpointTypesByChannelType(constant.ChannelTypeOpenAI, modelName)
		if !endpointTypeContains(endpoints, constant.EndpointTypeOpenAIVideo) {
			t.Fatalf("model %q endpoints = %v, missing %q", modelName, endpoints, constant.EndpointTypeOpenAIVideo)
		}
	}

	chatEndpoints := GetEndpointTypesByChannelType(constant.ChannelTypeOpenAI, "grok-4.5")
	if endpointTypeContains(chatEndpoints, constant.EndpointTypeOpenAIVideo) {
		t.Fatalf("chat model endpoints = %v, unexpectedly include video", chatEndpoints)
	}
}
