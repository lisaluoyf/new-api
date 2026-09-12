package i18n

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSupportedLanguages(t *testing.T) {
	want := []string{
		"zh-CN", "zh-TW", "en", "ja", "pt", "de", "fr", "tr",
		"it", "pl", "id", "ko", "es", "ru", "vi",
	}
	if got := SupportedLanguages(); !reflect.DeepEqual(got, want) {
		t.Fatalf("SupportedLanguages() = %v, want %v", got, want)
	}
}

func TestLocaleFilesMatchEnglishKeys(t *testing.T) {
	english := readLocaleKeys(t, "en")
	for _, lang := range SupportedLanguages() {
		fileLang := lang
		if lang == LangZhCN {
			fileLang = "zh-CN"
		}
		keys := readLocaleKeys(t, fileLang)
		if !reflect.DeepEqual(keys, english) {
			t.Errorf("locale %s key set differs from English", lang)
		}
	}
}

func readLocaleKeys(t *testing.T, lang string) []string {
	t.Helper()
	content, err := localeFS.ReadFile(fmt.Sprintf("locales/%s.yaml", lang))
	if err != nil {
		t.Fatal(err)
	}
	messages := map[string]string{}
	if err := yaml.Unmarshal(content, &messages); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(messages))
	for key := range messages {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func TestLanguageNormalization(t *testing.T) {
	tests := map[string]string{
		"ru-RU,ru;q=0.9":          LangRu,
		"ja-JP":                   LangJa,
		"de-DE":                   LangDe,
		"es-ES":                   LangEs,
		"zh-HK":                   LangZhTW,
		"zh-MO":                   LangZhTW,
		"zh-Hant":                 LangZhTW,
		"unknown":                 LangEn,
		"xx-YY,ja-JP;q=0.9":       LangJa,
		"de-DE;q=0.4,en-US;q=0.8": LangEn,
		"japanese,en-US;q=0.9":    LangEn,
		"zh-Hant-TW":              LangZhTW,
		"ja-!,de-DE;q=0.9":        LangDe,
		"ru-RU;q=2,en-US;q=0.8":   LangEn,
	}
	for header, want := range tests {
		if got := ParseAcceptLanguage(header); got != want {
			t.Errorf("ParseAcceptLanguage(%q) = %q, want %q", header, got, want)
		}
	}
}

func TestAllLanguageBundlesLoadAndFallback(t *testing.T) {
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	for _, lang := range SupportedLanguages() {
		if got := Translate(lang, MsgOperationFailed); got == "" || got == MsgOperationFailed {
			t.Errorf("Translate(%q, %q) = %q", lang, MsgOperationFailed, got)
		}
	}
	if got := Translate("xx-YY", MsgOperationFailed); got != "Operation failed" {
		t.Fatalf("unknown language fallback = %q, want English", got)
	}
}

func TestIsSupportedRejectsUnknownLanguages(t *testing.T) {
	for _, lang := range []string{"japanese", "xx-YY", ""} {
		if IsSupported(lang) {
			t.Errorf("IsSupported(%q) = true, want false", lang)
		}
	}
	for _, lang := range []string{"ja-JP", "zh-Hant-TW", "de"} {
		if !IsSupported(lang) {
			t.Errorf("IsSupported(%q) = false, want true", lang)
		}
	}
}
