package service

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/require"
)

func TestTrialLimitNotificationCopyAndRedirect(t *testing.T) {
	oldAddress := system_setting.ServerAddress
	oldAmount := common.FirstTopupPromoAmount
	oldDiscount := common.FirstTopupPromoDiscount
	system_setting.ServerAddress = "https://apimaster.ai"
	common.FirstTopupPromoAmount = 10
	common.FirstTopupPromoDiscount = 0.85
	t.Cleanup(func() {
		system_setting.ServerAddress = oldAddress
		common.FirstTopupPromoAmount = oldAmount
		common.FirstTopupPromoDiscount = oldDiscount
	})

	standard, standardButton := renderGPTTrialLimitNotification(model.TrialLimitNotificationStandard, "zh-CN")
	require.Contains(t, standard, "GPT 体验额度即将用尽")
	require.Contains(t, standard, "GPT-5.6 可节省 97%")
	require.Less(t,
		strings.Index(standard, "体验额度按官方价格计费"),
		strings.Index(standard, "充值后，无需重新配置"),
	)
	require.Equal(t, "🚀 立即充值并继续使用", standardButton)

	promo, promoButton := renderGPTTrialLimitNotification(model.TrialLimitNotificationFirstTopupPromo, "zh-CN")
	require.True(t, strings.Contains(promo, "首充限时优惠"))
	require.Contains(t, promo, "充值 $10，仅需支付 $8.50（85 折）")
	require.Equal(t, "🚀 立即充值，享 85 折", promoButton)
	require.Equal(t, "https://apimaster.ai/api/trial-limit/redirect/opaque-token", trialLimitNotificationRedirectURL("opaque-token"))
}

func TestTrialLimitNotificationCopySupportsAllLocales(t *testing.T) {
	locales := []string{"zh-CN", "zh-TW", "en", "ja", "pt", "de", "fr", "tr", "it", "pl", "id", "ko", "es", "ru", "vi"}
	for _, locale := range locales {
		text, button := renderGPTTrialLimitNotification(model.TrialLimitNotificationStandard, locale)
		require.NotEmpty(t, text, locale)
		require.NotEmpty(t, button, locale)
	}
	english, _ := renderGPTTrialLimitNotification(model.TrialLimitNotificationStandard, "xx")
	require.Contains(t, english, "Your GPT trial credit")
}

func TestParseNumericSocialIDRejectsLegacyPlaceholder(t *testing.T) {
	value, ok := parseNumericSocialID("newapi:123")
	require.False(t, ok)
	require.Empty(t, value)
	value, ok = parseNumericSocialID(" 123456 ")
	require.True(t, ok)
	require.Equal(t, "123456", value)
}
