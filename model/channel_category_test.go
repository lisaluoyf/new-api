package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestChannelCategorySelectionAndEdits(t *testing.T) {
	previousDB, previousCol := DB, commonGroupCol
	t.Cleanup(func() { DB, commonGroupCol = previousDB, previousCol })
	var err error
	DB, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	commonGroupCol = "`group`"
	require.NoError(t, DB.AutoMigrate(&Channel{}, &Ability{}))
	for index, category := range []*string{nil, common.GetPointer(""), common.GetPointer("cloud"), common.GetPointer("official")} {
		require.NoError(t, DB.Create(&Channel{Id: index + 1, Key: "test", Name: "route", Models: "category-test", Group: "default", ChannelCategory: category}).Error)
	}
	for _, test := range []struct {
		ids        []int
		categories []string
		include    bool
		want       []int
	}{
		{nil, []string{"default"}, true, []int{1, 2}},
		{[]int{1, 4}, []string{"official"}, true, []int{1, 4}},
		{nil, []string{"cloud", "official"}, true, []int{3, 4}},
		{[]int{1}, []string{"official"}, false, []int{2, 3}},
		{nil, nil, true, []int{}},
		{nil, nil, false, []int{1, 2, 3, 4}},
	} {
		got := []int{}
		require.NoError(t, ApplyChannelCategorySelection(DB.Model(&Channel{}), "id", "channel_category", test.ids, test.categories, test.include).Order("id").Pluck("id", &got).Error)
		require.Equal(t, test.want, got)
	}
	patch := Channel{Id: 4, Name: "renamed"}
	require.NoError(t, patch.Update())
	require.Equal(t, "official", patch.GetChannelCategory())
	patch.ChannelCategory = common.GetPointer("")
	require.NoError(t, patch.Update())
	require.Equal(t, "default", patch.GetChannelCategory())
	scope := ProviderChannelPickScope([]int{1}, []string{"cloud"}, true)
	require.Equal(t, []int{1, 3}, scope.IDs)
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", 2).Update("channel_category", "cloud").Error)
	require.Equal(t, []int{1, 2, 3}, ProviderChannelPickScope([]int{1}, []string{"cloud"}, true).IDs)
}

func TestChannelCategorySwitchValidationAndPersistence(t *testing.T) {
	previousDB := DB
	common.OptionMapRWMutex.Lock()
	previousOptions := common.OptionMap
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		DB = previousDB
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
	})
	var err error
	DB, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, DB.AutoMigrate(&Option{}))
	require.False(t, ChannelCategoriesEnabled())
	for _, key := range []string{ChannelCategoriesEnabledOption, ChannelCategoriesPublicEnabledOption} {
		for _, invalid := range []string{"1", "yes", "TRUE", "", "null"} {
			require.Error(t, UpdateOption(key, invalid))
		}
		require.NoError(t, UpdateOption(key, "true"))
	}
	require.True(t, ChannelCategoriesEnabled())
	require.True(t, ChannelCategoriesPublicEnabled())
	require.NoError(t, UpdateOption(ChannelCategoriesEnabledOption, "false"))
	require.False(t, ChannelCategoriesEnabled())
	require.True(t, ChannelCategoriesPublicEnabled())
	var saved Option
	require.NoError(t, DB.Where(&Option{Key: ChannelCategoriesEnabledOption}).First(&saved).Error)
	require.Equal(t, "false", saved.Value)
	// Rehydrate through the same option map update used during startup/sync.
	require.NoError(t, updateOptionMap(ChannelCategoriesEnabledOption, saved.Value))
	require.False(t, ChannelCategoriesEnabled())
}
