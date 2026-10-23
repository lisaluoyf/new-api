package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestKlingV4AccountingMatchesChargedQuota(t *testing.T) {
	oldDB, oldLogDB, oldOptions := model.DB, model.LOG_DB, common.OptionMap
	oldPrices := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() {
		model.DB, model.LOG_DB, common.OptionMap = oldDB, oldLogDB, oldOptions
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(oldPrices))
	})
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"kling-v4":0.1}`))
	common.OptionMap = map[string]string{"VideoModelPricing": `{"kling-v4":{"base_variant":"720P","base_price":0.1,"prices":{"720P":0.1,"1080P":0.3,"720P-input":0.08},"official_prices":{"720P":0.1,"1080P":0.3,"720P-input":0.08}}}`}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB, model.LOG_DB = db, db
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Channel{}, &model.ChannelModelPricing{}, &model.Log{}))
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "kling-v4-cost", AffCode: "kling-v4-cost"}).Error)
	setting := `{"manual_group_ratio":0.35,"model_group_ratios":{"kling-v4":0.35}}`
	one := 1.0
	require.NoError(t, db.Create(&model.Channel{Id: 306, Setting: &setting, RechargeRate: &one, ApimasterPriceRatio: &one}).Error)
	for _, tc := range []struct {
		variant           string
		ratio, unit, cost float64
	}{{"720P", 1, .1, .28}, {"1080P", 3, .3, .84}, {"720P-input", .8, .08, .224}} {
		t.Run(tc.variant, func(t *testing.T) {
			quota := int(tc.unit * 8 * 1.05 * 500000)
			got := BuildConsumeAccountingFields(ConsumeAccountingInput{UserId: 1, ChannelId: 306, ModelName: "kling-v4", BillingMode: accountingBillingModeDurationSeconds, DurationSeconds: 8, DurationUnitPrice: tc.unit, DurationPriceVariant: tc.variant, GroupRatio: 1.05, Quota: quota})
			require.Equal(t, "ok", got.Status, got.Snapshot)
			require.InDelta(t, tc.cost, got.ChannelCostAmountUSD, 1e-9)
			require.InDelta(t, float64(quota)/500000, got.UserFinalAmountUSD, 2e-6)
		})
	}
}
