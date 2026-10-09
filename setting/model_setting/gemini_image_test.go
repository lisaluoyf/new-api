package model_setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGeminiImageModelsWithLegacyOperatorSettings(t *testing.T) {
	previous := geminiSettings.SupportedImagineModels
	t.Cleanup(func() { geminiSettings.SupportedImagineModels = previous })
	geminiSettings.SupportedImagineModels = []string{"operator-image-model"}
	for _, model := range []string{"gemini-nano-banana-2.1", "gemini-nano-banana-2.1-b", "operator-image-model"} {
		require.True(t, IsGeminiModelSupportImagine(model), model)
	}
	for _, model := range []string{"gemini-nano-banana-2.1-unknown", "gemini-2.5-pro", ""} {
		require.False(t, IsGeminiModelSupportImagine(model), model)
	}
}
