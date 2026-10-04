package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDailyStatsDayStartUsesBeijingCalendarDay(t *testing.T) {
	instant := time.Date(2026, 9, 22, 0, 30, 0, 0, time.UTC)
	want := time.Date(2026, 9, 21, 16, 0, 0, 0, time.UTC).Unix()
	require.Equal(t, want, dailyStatsDayStart(instant.Unix()))
	require.Equal(t, "2026-09-22", dailyStatsDayKey(dailyStatsDayStart(instant.Unix())))
}

func TestDailyStatsPaidAmountMatchesDailyReportRules(t *testing.T) {
	tests := []struct {
		name string
		row  dailyStatsTopupRow
		want float64
	}{
		{
			name: "immutable snapshot wins",
			row: dailyStatsTopupRow{
				PaidAmountUSD:   8.5,
				Money:           10,
				PaymentProvider: PaymentProviderStripe,
			},
			want: 8.5,
		},
		{
			name: "explicit zero snapshot does not fall back",
			row: dailyStatsTopupRow{
				PaidAmountUSDSource: "settlement",
				Money:               10,
				PaymentProvider:     PaymentProviderStripe,
			},
			want: 0,
		},
		{
			name: "legacy USD provider falls back to money",
			row: dailyStatsTopupRow{
				Money:           25,
				PaymentProvider: PaymentProviderPayPal,
			},
			want: 25,
		},
		{
			name: "non USD legacy provider has no fallback",
			row: dailyStatsTopupRow{
				Money:           100,
				PaymentProvider: PaymentProviderPlatega,
			},
			want: 0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, dailyStatsPaidAmount(test.row))
		})
	}
}

func TestTodayPaymentSummaryMatchesDailyStatsCashReceipts(t *testing.T) {
	previousDB := DB
	t.Cleanup(func() { DB = previousDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&TopUp{}))
	DB = db

	dayStart := time.Date(2026, 10, 3, 16, 0, 0, 0, time.UTC).Unix()
	now := time.Unix(dayStart+10*3600, 0)
	require.NoError(t, db.Create(&[]TopUp{
		{TradeNo: "yesterday", UserId: 9, Money: 99, PaymentProvider: PaymentProviderPayPal, CompleteTime: dayStart - 1, Status: common.TopUpStatusSuccess},
		{TradeNo: "discounted-wallet", UserId: 1, PaidAmountUSD: 8.5, CreditedAmount: 10, Money: 10, PaymentProvider: PaymentProviderStripe, CompleteTime: dayStart, Status: common.TopUpStatusSuccess},
		{TradeNo: "repeat-wallet", UserId: 1, PaidAmountUSD: 4, Money: 4, PaymentProvider: PaymentProviderPayPal, CompleteTime: dayStart + 60, Status: common.TopUpStatusSuccess},
		{TradeNo: "subscription", UserId: 2, PaidAmountUSD: 29.99, PaidAmountUSDSource: "subscription", Money: 29.99, CompleteTime: dayStart + 120, Status: common.TopUpStatusSuccess},
		{TradeNo: "legacy-usd", UserId: 3, Money: 2, PaymentProvider: PaymentProviderPayPal, CreateTime: dayStart + 180, Status: common.TopUpStatusSuccess},
		{TradeNo: "legacy-local-currency", UserId: 4, Money: 70, PaymentProvider: PaymentProviderEpay, CompleteTime: dayStart + 240, Status: common.TopUpStatusSuccess},
		{TradeNo: "explicit-zero", UserId: 5, Money: 10, PaidAmountUSDSource: "settlement", PaymentProvider: PaymentProviderStripe, CompleteTime: dayStart + 300, Status: common.TopUpStatusSuccess},
		{TradeNo: "free-credit", UserId: 6, Amount: 100, PaymentProvider: PaymentProviderFree, CompleteTime: dayStart + 360, Status: common.TopUpStatusSuccess},
		{TradeNo: "pending", UserId: 7, PaidAmountUSD: 500, CompleteTime: dayStart + 420, Status: common.TopUpStatusPending},
		{TradeNo: "future", UserId: 8, PaidAmountUSD: 500, CompleteTime: now.Unix() + 1, Status: common.TopUpStatusSuccess},
	}).Error)

	amount, users, err := todayPaymentSummary(now)
	require.NoError(t, err)
	require.InDelta(t, 44.49, amount, 0.000001)
	require.Equal(t, 5, users)

	amount, users, err = todayPaymentSummary(time.Unix(dayStart-1, 0))
	require.NoError(t, err)
	require.Equal(t, float64(99), amount)
	require.Equal(t, 1, users)

	require.NoError(t, db.Migrator().DropTable(&TopUp{}))
	_, _, err = todayPaymentSummary(now)
	require.Error(t, err)
}
