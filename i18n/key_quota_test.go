package i18n

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKeyQuotaMessageLocalizedForEveryLanguage(t *testing.T) {
	require.NoError(t, Init())
	english := Translate(LangEn, MsgTokenQuotaInsufficient)
	require.Contains(t, english, "API key")
	require.Contains(t, english, "Account balance")
	for _, lang := range SupportedLanguages() {
		message := Translate(lang, MsgTokenQuotaInsufficient)
		require.NotEmpty(t, message)
		require.NotEqual(t, MsgTokenQuotaInsufficient, message)
		if lang != LangEn {
			require.NotEqual(t, english, message, "locale %s fell back to English", lang)
		}
	}
}
