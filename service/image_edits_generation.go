package service

// GptImage2EditsViaGenerations identifies upstreams whose image-to-image API
// accepts JSON image_urls at /images/generations instead of multipart edits.
// Keep the original client path/body intact: a later fallback may use native edits.
func GptImage2EditsViaGenerations(channelID int, modelName string) bool {
	if NormalizeGptImage2ModelName(modelName) != gptImage2CanonicalModel {
		return false
	}
	switch channelID {
	case 59, 81, 149:
		return true
	default:
		return false
	}
}
