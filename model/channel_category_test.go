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
