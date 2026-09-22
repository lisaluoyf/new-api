package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
