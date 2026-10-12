package model

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestClaudeCacheMigrationIsTargetedAndRunsOnce(t *testing.T) {
	oldDB := DB
	oldRatios := ratio_setting.CacheRatio2JSONString()
	common.OptionMapRWMutex.Lock()
	oldOptions := common.OptionMap
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		DB = oldDB
		require.NoError(t, ratio_setting.UpdateCacheRatioByJSONString(oldRatios))
		common.OptionMapRWMutex.Lock()
		common.OptionMap = oldOptions
		common.OptionMapRWMutex.Unlock()
	})
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	require.NoError(t, db.AutoMigrate(&Option{}))
	require.NoError(t, ratio_setting.UpdateCacheRatioByJSONString(`{"claude-sonnet-5-5":0.1,"claude-sonnet-5.5":0.075,"claude-sonnet-5":0.1}`))
	require.NoError(t, migrateClaudeCachePricing())
	ratio, _ := ratio_setting.GetCacheRatio("claude-sonnet-5-5")
	require.Equal(t, .05, ratio)
	ratio, _ = ratio_setting.GetCacheRatio("claude-sonnet-5.5")
	require.Equal(t, .075, ratio)
	ratio, _ = ratio_setting.GetCacheRatio("claude-sonnet-5")
	require.Equal(t, .1, ratio)
	require.NoError(t, UpdateOption("CacheRatio", `{"claude-sonnet-5-5":0.1}`))
	require.NoError(t, migrateClaudeCachePricing())
	ratio, _ = ratio_setting.GetCacheRatio("claude-sonnet-5-5")
	require.Equal(t, .1, ratio)
}
