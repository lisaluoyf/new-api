package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestImageAccountingUsesMediaPricingAndChannelCoefficients(t *testing.T) {
	oldDB, oldOptions := model.DB, common.OptionMap
	t.Cleanup(func() { model.DB = oldDB; common.OptionMap = oldOptions })
	common.OptionMap = map[string]string{ratio_setting.ImageModelPricingOption: ratio_setting.DefaultImageModelPricingJSON()}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.ChannelModelPricing{}, &model.User{}))
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "image-accounting"}).Error)
	for _, tc := range []struct {
		name, settings, variant           string
		markup, recharge, group, wantCost float64
		n                                 int
	}{
		{"59 no upstream price", `{"manual_group_ratio":0}`, "1K", 1, 1, 1.05, .25, 1},
		{"81 mapped model discount", `{"manual_group_ratio":0.05}`, "2K", 2, 1, 1.05, .015, 1},
		{"102 markup multi image", `{"manual_group_ratio":0}`, "4K", 1.3, 1, 1.05, 1.2, 2},
		{"model override recharge", `{"manual_group_ratio":0.05,"model_group_ratios":{"gpt-image-2.5-flare":0.2}}`, "2K", 2, .5, 1.05, .06, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mapping := `{"gpt-image-2.5-flare":"gpt-image-2.5-ext"}`
			require.NoError(t, db.Where("1=1").Delete(&model.Channel{}).Error)
			require.NoError(t, db.Create(&model.Channel{Id: 81, Setting: &tc.settings, ModelMapping: &mapping, RechargeRate: &tc.recharge, ApimasterPriceRatio: &tc.markup}).Error)
			// Conflicting token-price rows must not replace the media table used at settlement.
			require.NoError(t, model.UpsertChannelModelPricings([]model.ChannelModelPricing{{ChannelId: 81, ModelName: "gpt-image-2.5-ext", InputPrice: 999, GroupRatio: 1}}))
			quota := int(tc.wantCost*tc.markup*tc.group*common.QuotaPerUnit + .5)
			got := BuildConsumeAccountingFields(ConsumeAccountingInput{UserId: 1, ChannelId: 81, ModelName: "gpt-image-2.5-flare", BillingMode: accountingBillingModeImageCount, ImageCount: tc.n, ImagePriceVariant: tc.variant, GroupRatio: tc.group, Quota: quota})
			require.Equal(t, "ok", got.Status, got.Snapshot)
			require.InDelta(t, tc.wantCost, got.ChannelCostAmountUSD, 1e-9)
			require.InDelta(t, float64(quota)/common.QuotaPerUnit, got.UserFinalAmountUSD, 1e-9)
			var snap consumeAccountingSnapshot
			require.NoError(t, common.UnmarshalJsonStr(got.Snapshot, &snap))
			require.Equal(t, tc.variant, snap.ImagePriceVariant)
			require.Greater(t, snap.ImageBaseUnits, 0.0)
		})
	}
	// Missing token pricing must still be reported instead of silently borrowing image prices.
	got := BuildConsumeAccountingFields(ConsumeAccountingInput{UserId: 1, ChannelId: 81, ModelName: "unknown-token-model", InputTokens: 100, Quota: 1})
	require.Equal(t, "partial", got.Status)
	require.Contains(t, got.Snapshot, "channel_cost_price_missing")
}
