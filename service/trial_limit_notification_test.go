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

	standard, standardButton := renderGPTTrialLimitNotification(model.TrialLimitNotificationStandard)
	require.Contains(t, standard, "GPT 体验额度即将用尽")
	require.Contains(t, standard, "GPT-5.6 可节省 97%")
	require.Equal(t, "🚀 立即充值并继续使用", standardButton)

	promo, promoButton := renderGPTTrialLimitNotification(model.TrialLimitNotificationFirstTopupPromo)
	require.True(t, strings.Contains(promo, "首充限时 85 折"))
	require.Contains(t, promo, "充值 $10，仅需支付 $8.50")
	require.Equal(t, "🚀 立即充值，享 85 折", promoButton)
	require.Equal(t, "https://apimaster.ai/api/trial-limit/redirect/opaque-token", trialLimitNotificationRedirectURL("opaque-token"))
}

func TestParseNumericSocialIDRejectsLegacyPlaceholder(t *testing.T) {
	value, ok := parseNumericSocialID("newapi:123")
	require.False(t, ok)
	require.Empty(t, value)
	value, ok = parseNumericSocialID(" 123456 ")
	require.True(t, ok)
	require.Equal(t, "123456", value)
}
