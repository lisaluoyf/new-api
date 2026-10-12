package ratio_setting

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestClaudeCache1hConfigurationAndZero(t *testing.T) {
	oldHour := CreateCache1hRatio2JSONString()
	oldFive := CreateCacheRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, UpdateCreateCache1hRatioByJSONString(oldHour))
		require.NoError(t, UpdateCreateCacheRatioByJSONString(oldFive))
	})
	require.NoError(t, UpdateCreateCacheRatioByJSONString(`{"claude-sonnet-5-5":1.25}`))
	require.NoError(t, UpdateCreateCache1hRatioByJSONString(`{}`))
	require.InDelta(t, .8, CacheCreation1hRatioFromBase("claude-sonnet-5-5", .5), 1e-12)
	require.Nil(t, ResolveCacheCreation1hPrice("gpt-6-astra", 1.25, nil))
	require.NoError(t, UpdateCreateCache1hRatioByJSONString(`{"claude-sonnet-5-5":3}`))
	require.InDelta(t, 1.2, CacheCreation1hRatioFromBase("claude-sonnet-5-5", .5), 1e-12)
	require.NoError(t, UpdateCreateCache1hRatioByJSONString(`{"claude-sonnet-5-5":0}`))
	require.Zero(t, CacheCreation1hRatioFromBase("claude-sonnet-5-5", .5))
	require.Error(t, UpdateCreateCache1hRatioByJSONString(`{"claude-sonnet-5-5":-1}`))
	require.Error(t, UpdateCreateCache1hRatioByJSONString(`{"claude-sonnet-5-5":"bad"}`))
}
