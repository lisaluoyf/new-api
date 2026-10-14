package i18n

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVideoEndpointMessageLocalizedForEveryLanguage(t *testing.T) {
	require.NoError(t, Init())
	args := map[string]any{"Model": "seedance-2.0-mini"}
	english := Translate(LangEn, MsgDistributorVideoModelTextEndpoint, args)
	for _, lang := range SupportedLanguages() {
		message := Translate(lang, MsgDistributorVideoModelTextEndpoint, args)
		require.Contains(t, message, "seedance-2.0-mini")
		require.Contains(t, message, "POST /v1/videos/generations")
		if lang != LangEn {
			require.NotEqual(t, english, message, "locale %s fell back to English", lang)
		}
	}
}
