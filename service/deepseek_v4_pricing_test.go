package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func beijingTime(t *testing.T, hour, minute int) time.Time {
	t.Helper()
	return time.Date(2026, time.August, 17, hour, minute, 0, 0, deepSeekV4Timezone)
}

func TestDeepSeekV4OfficialPricingAtBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		at        time.Time
		wantInput float64
	}{
		{name: "before morning peak", at: beijingTime(t, 8, 59), wantInput: 0.22},
		{name: "morning peak starts", at: beijingTime(t, 9, 0), wantInput: 0.44},
		{name: "morning peak ends", at: beijingTime(t, 12, 0), wantInput: 0.22},
		{name: "afternoon peak starts", at: beijingTime(t, 14, 0), wantInput: 0.44},
		{name: "afternoon peak ends", at: beijingTime(t, 18, 0), wantInput: 0.22},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prices, ok := DeepSeekV4OfficialPricingAt("deepseek-v4-flash", tt.at)
			require.True(t, ok)
			require.InDelta(t, tt.wantInput, prices.InputPrice, 0.0000001)
			require.InDelta(t, tt.wantInput*3, prices.OutputPrice, 0.0000001)
		})
	}
}

func TestDeepSeekV4OfficialPricingAtPro(t *testing.T) {
	offPeak, ok := DeepSeekV4OfficialPricingAt("deepseek-v4-pro", beijingTime(t, 13, 0))
	require.True(t, ok)
	require.InDelta(t, 0.66, offPeak.InputPrice, 0.0000001)
	require.InDelta(t, 1.98, offPeak.OutputPrice, 0.0000001)
	require.InDelta(t, 0.022, offPeak.CachePrice, 0.0000001)
	require.InDelta(t, 0.66, offPeak.CacheCreationPrice, 0.0000001)

	peak, ok := DeepSeekV4OfficialPricingAt("deepseek-v4-pro", beijingTime(t, 15, 0))
	require.True(t, ok)
	require.InDelta(t, 1.32, peak.InputPrice, 0.0000001)
	require.InDelta(t, 3.96, peak.OutputPrice, 0.0000001)
	require.InDelta(t, 0.044, peak.CachePrice, 0.0000001)
}

func TestDeepSeekV4UserPricingAtUsesModelOverride(t *testing.T) {
	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.Exec(`CREATE TABLE channels (
		id integer primary key, apimaster_price_ratio real, model_price_ratios text
	)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO channels (id, apimaster_price_ratio, model_price_ratios)
		VALUES (1, 1.5, '{"deepseek-v4-flash":0.8}')`).Error)

	prices, ok := DeepSeekV4UserPricingAt(1, "deepseek-v4-flash", beijingTime(t, 13, 0))
	require.True(t, ok)
	require.InDelta(t, 0.176, prices.InputPrice, 0.0000001)
	require.InDelta(t, 0.528, prices.OutputPrice, 0.0000001)
	require.InDelta(t, 0.0056, prices.CachePrice, 0.0000001)
}

func TestDeepSeekV4OfficialPricingRejectsOtherModels(t *testing.T) {
	_, ok := DeepSeekV4OfficialPricingAt("deepseek-chat", beijingTime(t, 10, 0))
	require.False(t, ok)
}
