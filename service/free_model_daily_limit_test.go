package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestFreeModelDailyLimitResetsOnUTCDate(t *testing.T) {
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	resetFreeModelDailyLimitForTest()
	t.Cleanup(func() {
		common.RedisEnabled = oldRedis
		resetFreeModelDailyLimitForTest()
	})

	now := time.Date(2026, time.August, 21, 23, 59, 0, 0, time.UTC)
	freeModelDailyLimitNow = func() time.Time { return now }
	allowed, used, err := ReserveFreeModelDailyRequest(91, 1)
	require.NoError(t, err)
	require.True(t, allowed)
	require.Equal(t, 1, used)
	allowed, _, err = ReserveFreeModelDailyRequest(91, 1)
	require.NoError(t, err)
	require.False(t, allowed)

	now = now.Add(2 * time.Minute)
	allowed, used, err = ReserveFreeModelDailyRequest(91, 1)
	require.NoError(t, err)
	require.True(t, allowed)
	require.Equal(t, 1, used)
}

func TestFreeModelDailyLimitGroupIsSharedAcrossChannels(t *testing.T) {
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	resetFreeModelDailyLimitForTest()
	t.Cleanup(func() {
		common.RedisEnabled = oldRedis
		resetFreeModelDailyLimitForTest()
	})

	allowed, used, err := ReserveFreeModelDailyRequest(91, 2, "requesty")
	require.NoError(t, err)
	require.True(t, allowed)
	require.Equal(t, 1, used)
	allowed, used, err = ReserveFreeModelDailyRequest(92, 2, "requesty")
	require.NoError(t, err)
	require.True(t, allowed)
	require.Equal(t, 2, used)
	allowed, used, err = ReserveFreeModelDailyRequest(93, 2, "requesty")
	require.NoError(t, err)
	require.False(t, allowed)
	require.Equal(t, 2, used)

	allowed, used, err = ReserveFreeModelDailyRequest(93, 2, "another-provider")
	require.NoError(t, err)
	require.True(t, allowed)
	require.Equal(t, 1, used)
}
