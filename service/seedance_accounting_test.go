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

func TestSeedanceAccountingVariantCostAndFrozenCompletion(t *testing.T) {
	previousPrices := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(previousPrices)) })
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"seedance-2.5":0.23112,"seedance-2.0-mini":0.0756}`))
	oldDB, oldLogDB, oldOptions := model.DB, model.LOG_DB, common.OptionMap
	t.Cleanup(func() { model.DB, model.LOG_DB, common.OptionMap = oldDB, oldLogDB, oldOptions })
	common.OptionMap = map[string]string{ratio_setting.VideoModelPricingOption: ratio_setting.DefaultVideoModelPricingJSON()}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB, model.LOG_DB = db, db
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.ChannelModelPricing{}, &model.User{}, &model.Log{}))
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "seedance-cost"}).Error)
	setting := `{"manual_group_ratio":0.805,"model_group_ratios":{"seedance-2.0-mini":0.5}}`
	recharge, markup := 1.0, 1.18
	require.NoError(t, db.Create(&model.Channel{Id: 273, Setting: &setting, RechargeRate: &recharge, ApimasterPriceRatio: &markup}).Error)
	for _, tc := range []struct {
		name, variant string
		seconds       int
		cost, price   float64
		quota         int
	}{
		{"seedance-2.5", "480P", 30, 2.48262, .09764972, 1537983},
		{"seedance-2.0-mini", "480P", 15, .263625, .017575, 138403},
		{"seedance-2.5", "720P", 4, .7442064, .219540888, 461036},
		{"seedance-2.5", "1080P", 4, 1.8309564, .540132522, 1134278},
		{"seedance-2.5", "480P-input", 10, .4949945, .058409851, 306652},
	} {
		t.Run(tc.name+tc.variant, func(t *testing.T) {
			got := BuildConsumeAccountingFields(ConsumeAccountingInput{UserId: 1, ChannelId: 273, ModelName: tc.name, BillingMode: accountingBillingModeDurationSeconds, DurationSeconds: tc.seconds, DurationUnitPrice: tc.price, DurationPriceVariant: tc.variant, GroupRatio: 1.05, Quota: tc.quota})
			require.Equal(t, "ok", got.Status, got.Snapshot)
			require.InDelta(t, tc.cost, got.ChannelCostAmountUSD, 1e-9)
			require.InDelta(t, float64(tc.quota)/500000, got.UserFinalAmountUSD, 1e-9)
			taskID := "task_" + tc.name + tc.variant
			row := model.Log{UserId: 1, ChannelId: 273, ModelName: tc.name, Type: model.LogTypeConsume, Quota: tc.quota, Other: common.MapToJsonStr(map[string]any{"is_task": true, "task_id": taskID}), AccountingStatus: got.Status, AccountingSnapshot: got.Snapshot, AccountingChannelCostAmountUSD: got.ChannelCostAmountUSD, AccountingUserFinalAmountUSD: got.UserFinalAmountUSD}
			require.NoError(t, db.Create(&row).Error)
			// A later catalog change must not reprice a frozen estimate.
			previous := common.OptionMap[ratio_setting.VideoModelPricingOption]
			common.OptionMap[ratio_setting.VideoModelPricingOption] = `{"seedance-2.5":{"official_prices":{"480P":9}}}`
			task := &model.Task{UserId: 1, TaskID: taskID, Status: model.TaskStatusSuccess, Data: []byte(`{"data":{"billing":{"currency":"CNY","reservation_status":"captured","charged_amount":"16.8"}}}`)}
			require.NoError(t, backfillSeedanceEstimatedCost(task, tc.seconds+1))
			require.NoError(t, backfillSeedanceEstimatedCost(task, tc.seconds+1))
			common.OptionMap[ratio_setting.VideoModelPricingOption] = previous
			require.NoError(t, db.First(&row, row.Id).Error)
			require.InDelta(t, tc.cost/float64(tc.seconds)*float64(tc.seconds+1), row.AccountingChannelCostAmountUSD, 1e-9)
			require.Equal(t, tc.quota, row.Quota)
			require.Equal(t, got.UserFinalAmountUSD, row.AccountingUserFinalAmountUSD)
			require.Contains(t, row.AccountingSnapshot, `"currency":"CNY"`)
			// A task accepted before deployment has only the base cost snapshot.
			var legacy map[string]any
			require.NoError(t, common.UnmarshalJsonStr(got.Snapshot, &legacy))
			ratio := legacy["duration_price_ratio"].(float64)
			legacy["prices"].(map[string]any)["channel_cost"].(map[string]any)["input_price"] = tc.cost / float64(tc.seconds) / ratio
			delete(legacy, "cost_price_source")
			delete(legacy, "duration_price_ratio")
			delete(legacy, "duration_price_variant")
			require.NoError(t, db.Model(&row).Update("accounting_snapshot", common.MapToJsonStr(legacy)).Error)
			task.PrivateData.SeedanceRequest = map[string]any{"billing_variant": tc.variant}
			task.Properties.OriginModelName = tc.name
			require.NoError(t, backfillSeedanceEstimatedCost(task, tc.seconds))
			require.NoError(t, backfillSeedanceEstimatedCost(task, tc.seconds))
			require.NoError(t, db.First(&row, row.Id).Error)
			require.InDelta(t, tc.cost, row.AccountingChannelCostAmountUSD, 1e-9)
			// A confirmed upstream USD receipt takes precedence over every estimate.
			require.NoError(t, model.UpdateLogChannelCostByTaskID(1, taskID, .123))
			require.NoError(t, backfillSeedanceEstimatedCost(task, 99))
			require.NoError(t, db.First(&row, row.Id).Error)
			require.InDelta(t, .123, row.AccountingChannelCostAmountUSD, 1e-9)
			require.NoError(t, db.Model(&row).Update("accounting_status", "refunded").Error)
			require.NoError(t, backfillSeedanceEstimatedCost(task, 100))
			require.NoError(t, db.First(&row, row.Id).Error)
			require.InDelta(t, .123, row.AccountingChannelCostAmountUSD, 1e-9)
		})
	}
	missing := BuildConsumeAccountingFields(ConsumeAccountingInput{UserId: 1, ChannelId: 273, ModelName: "seedance-2.5", BillingMode: accountingBillingModeDurationSeconds, DurationSeconds: 4, DurationPriceVariant: "8K"})
	require.Equal(t, "partial", missing.Status)
	require.Contains(t, missing.Snapshot, "video_procurement_variant_missing")
}
