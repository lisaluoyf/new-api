package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProviderScopeFiltersBeforePrioritySelection(t *testing.T) {
	oldDB, oldMemoryCache := DB, common.MemoryCacheEnabled
	oldSQLite, oldPostgreSQL, oldMySQL := common.UsingSQLite, common.UsingPostgreSQL, common.UsingMySQL
	oldGroups, oldChannels := group2model2channels, channelsIDM
	oldGroupColumn := commonGroupCol
	t.Cleanup(func() {
		DB, common.MemoryCacheEnabled = oldDB, oldMemoryCache
		common.UsingSQLite, common.UsingPostgreSQL, common.UsingMySQL = oldSQLite, oldPostgreSQL, oldMySQL
		group2model2channels, channelsIDM = oldGroups, oldChannels
		commonGroupCol = oldGroupColumn
	})
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	common.UsingSQLite, common.UsingPostgreSQL, common.UsingMySQL = true, false, false
	commonGroupCol = "`group`"
	require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}))
	for channelID := 1; channelID <= 3; channelID++ {
		priority := int64(100 - channelID)
		channel := Channel{Id: channelID, Name: "test-route", Key: "test-key", Models: "scope-test", Group: "scope-group", Status: 1, Priority: &priority}
		require.NoError(t, db.Create(&channel).Error)
		require.NoError(t, channel.AddAbilities(nil))
	}
	for _, useMemory := range []bool{false, true} {
		common.MemoryCacheEnabled = useMemory
		if useMemory {
			InitChannelCache()
		}
		for _, scope := range []*ChannelPickScope{
			{Include: true, IDs: []int{2, 3}},
			{Include: false, IDs: []int{1}},
		} {
			channel, err := GetRandomSatisfiedChannel("scope-group", "scope-test", 0, nil, scope)
			require.NoError(t, err)
			require.NotNil(t, channel)
			require.Equal(t, 2, channel.Id)
			channel, err = GetRandomSatisfiedChannel("scope-group", "scope-test", 1, nil, scope)
			require.NoError(t, err)
			require.NotNil(t, channel)
			require.Equal(t, 3, channel.Id)
		}
		channel, err := GetRandomSatisfiedChannel("scope-group", "scope-test", 0, nil, &ChannelPickScope{Include: true, IDs: []int{1, 2, 3}}, &ChannelPickScope{Include: false, IDs: []int{1, 2}})
		require.NoError(t, err)
		require.NotNil(t, channel)
		require.Equal(t, 3, channel.Id)
		channel, err = GetRandomSatisfiedChannel("scope-group", "scope-test", 0, nil, &ChannelPickScope{Include: true, IDs: []int{99}})
		require.NoError(t, err)
		require.Nil(t, channel)
		channel, err = GetRandomSatisfiedChannel("scope-group", "scope-test", 0, nil)
		require.NoError(t, err)
		require.Equal(t, 1, channel.Id)
		// A protocol filter must also run before priority selection, alone or
		// combined with provider/resolution scopes, in both DB and cache modes.
		filter := func(ch *Channel) bool { return ch.Id != 1 }
		for retry := 0; retry < 2; retry++ {
			picked, err := GetRandomSatisfiedChannel("scope-group", "scope-test", retry, filter)
			require.NoError(t, err)
			require.NotNil(t, picked)
			require.Equal(t, 2+retry, picked.Id)
		}
		picked, err := GetRandomSatisfiedChannel("scope-group", "scope-test", 0, filter, &ChannelPickScope{Include: false, IDs: []int{3}})
		require.NoError(t, err)
		require.NotNil(t, picked)
		require.Equal(t, 2, picked.Id)
		picked, err = GetRandomSatisfiedChannel("scope-group", "scope-test", 0, func(*Channel) bool { return false })
		require.NoError(t, err)
		require.Nil(t, picked)
	}
}
