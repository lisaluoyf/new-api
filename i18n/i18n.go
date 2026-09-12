package i18n

import (
	"embed"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
	"gopkg.in/yaml.v3"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
)

const (
	LangZhCN    = "zh-CN"
	LangZhTW    = "zh-TW"
	LangEn      = "en"
	LangJa      = "ja"
	LangPt      = "pt"
	LangDe      = "de"
	LangFr      = "fr"
	LangTr      = "tr"
	LangIt      = "it"
	LangPl      = "pl"
	LangId      = "id"
	LangKo      = "ko"
	LangEs      = "es"
	LangRu      = "ru"
	LangVi      = "vi"
	DefaultLang = LangEn // Fallback to English if language not supported
)

//go:embed locales/*.yaml
var localeFS embed.FS

var (
	bundle             *i18n.Bundle
	localizers         = make(map[string]*i18n.Localizer)
	mu                 sync.RWMutex
	initOnce           sync.Once
	languageTagPattern = regexp.MustCompile(`^[A-Za-z]{2,3}(?:[-_][A-Za-z0-9]{2,8})*$`)
)

// Init initializes the i18n bundle and loads all translation files
func Init() error {
	var initErr error
	initOnce.Do(func() {
		bundle = i18n.NewBundle(language.English)
		bundle.RegisterUnmarshalFunc("yaml", yaml.Unmarshal)

		// Load embedded translation files
		files := []string{
			"locales/zh-CN.yaml", "locales/zh-TW.yaml", "locales/en.yaml",
			"locales/ja.yaml", "locales/pt.yaml", "locales/de.yaml", "locales/fr.yaml",
			"locales/tr.yaml", "locales/it.yaml", "locales/pl.yaml", "locales/id.yaml",
			"locales/ko.yaml", "locales/es.yaml", "locales/ru.yaml", "locales/vi.yaml",
		}
		for _, file := range files {
			_, err := bundle.LoadMessageFileFS(localeFS, file)
			if err != nil {
				initErr = err
				return
			}
		}

		// Pre-create localizers for supported languages
		localizers[LangZhCN] = i18n.NewLocalizer(bundle, LangZhCN)
		localizers[LangZhTW] = i18n.NewLocalizer(bundle, LangZhTW)
		localizers[LangEn] = i18n.NewLocalizer(bundle, LangEn)
		localizers[LangJa] = i18n.NewLocalizer(bundle, LangJa, LangEn)
		localizers[LangPt] = i18n.NewLocalizer(bundle, LangPt, LangEn)
		localizers[LangDe] = i18n.NewLocalizer(bundle, LangDe, LangEn)
		localizers[LangFr] = i18n.NewLocalizer(bundle, LangFr, LangEn)
		localizers[LangTr] = i18n.NewLocalizer(bundle, LangTr, LangEn)
		localizers[LangIt] = i18n.NewLocalizer(bundle, LangIt, LangEn)
		localizers[LangPl] = i18n.NewLocalizer(bundle, LangPl, LangEn)
		localizers[LangId] = i18n.NewLocalizer(bundle, LangId, LangEn)
		localizers[LangKo] = i18n.NewLocalizer(bundle, LangKo, LangEn)
		localizers[LangEs] = i18n.NewLocalizer(bundle, LangEs, LangEn)
		localizers[LangRu] = i18n.NewLocalizer(bundle, LangRu, LangEn)
		localizers[LangVi] = i18n.NewLocalizer(bundle, LangVi, LangEn)

		// Set the TranslateMessage function in common package
		common.TranslateMessage = T
	})
	return initErr
}

// GetLocalizer returns a localizer for the specified language
func GetLocalizer(lang string) *i18n.Localizer {
	lang = normalizeLang(lang)

	mu.RLock()
	loc, ok := localizers[lang]
	mu.RUnlock()

	if ok {
		return loc
	}

	// Create new localizer for unknown language (fallback to default)
	mu.Lock()
	defer mu.Unlock()

	// Double-check after acquiring write lock
	if loc, ok = localizers[lang]; ok {
		return loc
	}

	loc = i18n.NewLocalizer(bundle, lang, DefaultLang)
	localizers[lang] = loc
	return loc
}

// T translates a message key using the language from gin context
func T(c *gin.Context, key string, args ...map[string]any) string {
	lang := GetLangFromContext(c)
	return Translate(lang, key, args...)
}

// Translate translates a message key for the specified language
func Translate(lang, key string, args ...map[string]any) string {
	loc := GetLocalizer(lang)

	config := &i18n.LocalizeConfig{
		MessageID: key,
	}

	if len(args) > 0 && args[0] != nil {
		config.TemplateData = args[0]
	}

	msg, err := loc.Localize(config)
	if err != nil {
		// Return key as fallback if translation not found
		return key
	}
	return msg
}

// userLangLoaderFunc is a function that loads user language from database/cache
// It's set by the model package to avoid circular imports
var userLangLoaderFunc func(userId int) string

// SetUserLangLoader sets the function to load user language (called from model package)
func SetUserLangLoader(loader func(userId int) string) {
	userLangLoaderFunc = loader
}

// GetLangFromContext extracts the language setting from gin context
// It checks multiple sources in priority order:
// 1. User settings (ContextKeyUserSetting) - if already loaded (e.g., by TokenAuth)
// 2. Lazy load user language from cache/DB using user ID
// 3. Language set by middleware (ContextKeyLanguage) - from Accept-Language header
// 4. Default language (English)
func GetLangFromContext(c *gin.Context) string {
	if c == nil {
		return DefaultLang
	}

	// 1. Try to get language from user settings (if already loaded by TokenAuth or other middleware)
	if userSetting, ok := common.GetContextKeyType[dto.UserSetting](c, constant.ContextKeyUserSetting); ok {
		if userSetting.Language != "" {
			if normalized, supported := NormalizeLanguage(userSetting.Language); supported {
				return normalized
			}
		}
	}

	// 2. Lazy load user language using user ID (for session-based auth where full settings aren't loaded)
	if userLangLoaderFunc != nil {
		if userId, exists := c.Get("id"); exists {
			if uid, ok := userId.(int); ok && uid > 0 {
				lang := userLangLoaderFunc(uid)
				if lang != "" {
					if normalized, supported := NormalizeLanguage(lang); supported {
						return normalized
					}
				}
			}
		}
	}

	// 3. Try to get language from context (set by I18n middleware from Accept-Language)
	if lang := c.GetString(string(constant.ContextKeyLanguage)); lang != "" {
		if normalized, supported := NormalizeLanguage(lang); supported {
			return normalized
		}
	}

	// 4. Try Accept-Language header directly (fallback if middleware didn't run)
	if acceptLang := c.GetHeader("Accept-Language"); acceptLang != "" {
		lang := ParseAcceptLanguage(acceptLang)
		if IsSupported(lang) {
			return lang
		}
	}

	return DefaultLang
}

// ParseAcceptLanguage parses the Accept-Language header and returns the preferred language
func ParseAcceptLanguage(header string) string {
	if header == "" {
		return DefaultLang
	}

	type candidate struct {
		lang string
		q    float64
		pos  int
	}
	candidates := make([]candidate, 0)
	for pos, part := range strings.Split(header, ",") {
		fields := strings.Split(part, ";")
		raw := strings.TrimSpace(fields[0])
		if raw == "" || raw == "*" {
			continue
		}
		quality := 1.0
		for _, field := range fields[1:] {
			parameter := strings.TrimSpace(field)
			name, value, found := strings.Cut(parameter, "=")
			if found && strings.EqualFold(strings.TrimSpace(name), "q") {
				parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
				if err != nil || parsed < 0 || parsed > 1 {
					quality = 0
					break
				}
				quality = parsed
			}
		}
		if quality <= 0 {
			continue
		}
		candidates = append(candidates, candidate{lang: raw, q: quality, pos: pos})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].q == candidates[j].q {
			return candidates[i].pos < candidates[j].pos
		}
		return candidates[i].q > candidates[j].q
	})
	for _, item := range candidates {
		if normalized, ok := normalizeKnownLang(item.lang); ok {
			return normalized
		}
	}
	return DefaultLang
}

func normalizeKnownLang(lang string) (string, bool) {
	if !languageTagPattern.MatchString(strings.TrimSpace(lang)) {
		return "", false
	}
	parts := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(lang)), func(r rune) bool {
		return r == '-' || r == '_'
	})
	if len(parts) == 0 || len(parts[0]) != 2 {
		return "", false
	}
	primary := parts[0]
	switch primary {
	case "zh":
		for _, part := range parts[1:] {
			if part == "tw" || part == "hk" || part == "mo" || part == "hant" {
				return LangZhTW, true
			}
		}
		return LangZhCN, true
	case "en":
		return LangEn, true
	case "ja":
		return LangJa, true
	case "pt":
		return LangPt, true
	case "de":
		return LangDe, true
	case "fr":
		return LangFr, true
	case "tr":
		return LangTr, true
	case "it":
		return LangIt, true
	case "pl":
		return LangPl, true
	case "id", "in":
		return LangId, true
	case "ko":
		return LangKo, true
	case "es":
		return LangEs, true
	case "ru":
		return LangRu, true
	case "vi":
		return LangVi, true
	default:
		return "", false
	}
}

// normalizeLang normalizes language code to supported format
func normalizeLang(lang string) string {
	if normalized, ok := normalizeKnownLang(lang); ok {
		return normalized
	}
	return DefaultLang
}

// NormalizeLanguage returns the canonical supported language code.
func NormalizeLanguage(lang string) (string, bool) {
	return normalizeKnownLang(lang)
}

// SupportedLanguages returns a list of supported language codes
func SupportedLanguages() []string {
	return []string{LangZhCN, LangZhTW, LangEn, LangJa, LangPt, LangDe, LangFr, LangTr, LangIt, LangPl, LangId, LangKo, LangEs, LangRu, LangVi}
}

// IsSupported checks if a language code is supported
func IsSupported(lang string) bool {
	lang, ok := normalizeKnownLang(lang)
	if !ok {
		return false
	}
	for _, supported := range SupportedLanguages() {
		if lang == supported {
			return true
		}
	}
	return false
}
