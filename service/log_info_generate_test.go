package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAppendChannelActualPriceUsesDeepSeekRequestStartTime(t *testing.T) {
	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.Exec(`CREATE TABLE channels (
		id integer primary key, model_mapping text, setting text, recharge_rate real,
		apimaster_price_ratio real, model_price_ratios text
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE channel_model_pricings (
		id integer primary key, channel_id integer not null, model_name text not null,
		input_price real, output_price real, cache_price real, cache_creation_price real,
		group_ratio real, pricing_source text
	)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO channels
		(id, recharge_rate, apimaster_price_ratio) VALUES (131, 0.8, 1.1)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO channel_model_pricings
		(channel_id, model_name, input_price, output_price, group_ratio, pricing_source)
		VALUES (131, 'deepseek-v4-flash', 9, 27, 1, 'api')`).Error)

	tests := []struct {
		name       string
		startHour  int
		wantInput  float64
		wantOutput float64
	}{
		{name: "off peak", startHour: 13, wantInput: 0.1936, wantOutput: 0.5808},
		{name: "peak", startHour: 15, wantInput: 0.3872, wantOutput: 1.1616},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			other := map[string]interface{}{}
			appendChannelActualPrice(&relaycommon.RelayInfo{
				ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: 131},
				OriginModelName: "deepseek-v4-flash",
				StartTime:       beijingTime(t, tt.startHour, 0),
			}, other)

			require.InDelta(t, tt.wantInput, other["ch_input_price"], 0.0000001)
			require.InDelta(t, tt.wantOutput, other["ch_output_price"], 0.0000001)
			if tt.startHour == 15 {
				require.Equal(t, "peak", other["ch_price_period"])
			} else {
				require.Equal(t, "off_peak", other["ch_price_period"])
			}
		})
	}
}

func TestAppendBillingInfoIncludesCodingPlanWalletFallbackReason(t *testing.T) {
	other := map[string]interface{}{}
	appendBillingInfo(&relaycommon.RelayInfo{
		BillingSource:               BillingSourceWallet,
		CodingPlanUnavailableReason: "official cache write price is unavailable",
	}, other)

	require.Equal(t, BillingSourceWallet, other["billing_source"])
	require.Equal(t, "official cache write price is unavailable", other["coding_plan_fallback_reason"])
}

func TestAppendBillingSourceInfoRecordsSubscriptionIdentityWithoutConsumption(t *testing.T) {
	other := map[string]interface{}{}
	AppendBillingSourceInfo(&relaycommon.RelayInfo{
		BillingSource:           BillingSourceSubscription,
		SubscriptionId:          2732,
		SubscriptionPlanId:      8,
		SubscriptionPlanTitle:   "Coding Starter",
		SubscriptionPlanType:    model.SubscriptionPlanTypeCodingPlan,
		SubscriptionCycleId:     86,
		SubscriptionPreConsumed: 361,
		SubscriptionPostDelta:   -316,
	}, other)

	require.Equal(t, BillingSourceSubscription, other["billing_source"])
	require.Equal(t, model.SubscriptionPlanTypeCodingPlan, other["subscription_type"])
	require.Equal(t, "Coding Starter", other["subscription_plan_title"])
	require.Equal(t, 2732, other["subscription_id"])
	require.Equal(t, 8, other["subscription_plan_id"])
	require.Equal(t, 86, other["subscription_cycle_id"])
	require.NotContains(t, other, "subscription_pre_consumed")
	require.NotContains(t, other, "subscription_post_delta")
	require.NotContains(t, other, "subscription_consumed")
}

func TestAppendBillingSourceInfoKeepsWalletIdentity(t *testing.T) {
	other := map[string]interface{}{}
	AppendBillingSourceInfo(&relaycommon.RelayInfo{
		BillingSource: BillingSourceWallet,
	}, other)

	require.Equal(t, BillingSourceWallet, other["billing_source"])
	require.NotContains(t, other, "subscription_type")
}

func TestAppendBillingSourceInfoRecordsGPTSubscriptionIdentity(t *testing.T) {
	other := map[string]interface{}{}
	AppendBillingSourceInfo(&relaycommon.RelayInfo{
		BillingSource:         BillingSourceSubscription,
		SubscriptionPlanTitle: "GPT Pro",
		SubscriptionPlanType:  model.SubscriptionPlanTypeGPTSubscription,
	}, other)

	require.Equal(t, BillingSourceSubscription, other["billing_source"])
	require.Equal(t, model.SubscriptionPlanTypeGPTSubscription, other["subscription_type"])
	require.Equal(t, "GPT Pro", other["subscription_plan_title"])
}
