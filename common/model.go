package common

import "strings"

var (
	// OpenAIResponseOnlyModels is a list of models that are only available for OpenAI responses.
	OpenAIResponseOnlyModels = []string{
		"o3-pro",
		"o3-deep-research",
		"o4-mini-deep-research",
	}
	ImageGenerationModels = []string{
		"dall-e-3",
		"dall-e-2",
		"gpt-image-1",
		"prefix:gpt-image-2", // gpt-image-2, gpt-image-2-official
		"prefix:imagen-",
		"prefix:gemini-3-pro-image",
		"flux-",
		"flux.1-",
		"flash-image", // gemini-2.5-flash-image, gemini-3.1-flash-image-preview, …
	}
	// VideoGenerationModels is the centralized capability registry for models
	// served through the OpenAI-compatible video endpoint. Use exact: or prefix:
	// rules so ordinary chat models are never classified by a loose substring.
	VideoGenerationModels = []string{
		"exact:minimax-h3",
		"exact:sora",
		"prefix:sora-2",
		"prefix:kling-",
		"prefix:doubao-seedance-",
		"exact:seedance-2.5",
		"prefix:grok-imagine-video",
		"prefix:grok-1.5-video-",
		"prefix:minimax-hailuo-",
		"prefix:t2v-01",
		"prefix:i2v-01",
		"prefix:s2v-01",
		"prefix:vidu",
		"prefix:veo-",
		"prefix:wan2.",
		"prefix:wanx2.",
		"prefix:jimeng_vgfm_",
	}
	OpenAITextModels = []string{
		"gpt-",
		"o1",
		"o3",
		"o4",
		"chatgpt",
	}
)

func IsOpenAIResponseOnlyModel(modelName string) bool {
	for _, m := range OpenAIResponseOnlyModels {
		if strings.Contains(modelName, m) {
			return true
		}
	}
	return false
}

func IsImageGenerationModel(modelName string) bool {
	modelName = strings.ToLower(modelName)
	for _, m := range ImageGenerationModels {
		if strings.HasPrefix(m, "prefix:") {
			if strings.HasPrefix(modelName, strings.TrimPrefix(m, "prefix:")) {
				return true
			}
			continue
		}
		if strings.Contains(modelName, m) {
			return true
		}
	}
	return false
}

func IsVideoGenerationModel(modelName string) bool {
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	for _, rule := range VideoGenerationModels {
		rule = strings.ToLower(rule)
		switch {
		case strings.HasPrefix(rule, "exact:"):
			if modelName == strings.TrimPrefix(rule, "exact:") {
				return true
			}
		case strings.HasPrefix(rule, "prefix:"):
			if strings.HasPrefix(modelName, strings.TrimPrefix(rule, "prefix:")) {
				return true
			}
		}
	}
	return false
}

// UsesAsyncImageTaskUpstream reports models whose upstream expects task submit + poll
// (APIMart-style: POST /v1/images/generations returns task_id; client or relay polls).
func UsesAsyncImageTaskUpstream(modelName string) bool {
	lower := strings.ToLower(strings.TrimSpace(modelName))
	if strings.HasPrefix(lower, "gpt-image-2") {
		return true
	}
	return strings.Contains(lower, "flash-image") || strings.HasPrefix(lower, "gemini-3-pro-image")
}

func IsOpenAITextModel(modelName string) bool {
	modelName = strings.ToLower(modelName)
	for _, m := range OpenAITextModels {
		if strings.Contains(modelName, m) {
			return true
		}
	}
	return false
}
