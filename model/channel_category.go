package model

import (
	"slices"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const ChannelCategoriesEnabledOption = "ChannelCategoriesEnabled"
const ChannelCategoriesPublicEnabledOption = "ChannelCategoriesPublicEnabled"

func ChannelCategoriesEnabled() bool {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	return common.OptionMap[ChannelCategoriesEnabledOption] == "true"
}

func ChannelCategoriesPublicEnabled() bool {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	return common.OptionMap[ChannelCategoriesPublicEnabledOption] == "true"
}

func ValidChannelCategory(category string) bool {
	return category == "" || category == "cloud" || category == "official"
}

func (channel *Channel) GetChannelCategory() string {
	if channel.ChannelCategory != nil && (*channel.ChannelCategory == "cloud" || *channel.ChannelCategory == "official") {
		return *channel.ChannelCategory
	}
	return "default"
}

// ApplyChannelCategorySelection applies the union before pagination, priority or
// price selection. Column names are internal constants, never request input.
func ApplyChannelCategorySelection(query *gorm.DB, idColumn, categoryColumn string, ids []int, categories []string, include bool) *gorm.DB {
	match := query.Session(&gorm.Session{NewDB: true}).Where("1 = 0")
	if len(ids) > 0 {
		match = match.Or(idColumn+" IN ?", ids)
	}
	if len(categories) > 0 {
		match = match.Or("COALESCE(NULLIF("+categoryColumn+", ''), 'default') IN ?", categories)
	}
	if include {
		return query.Where(match)
	}
	return query.Not(match)
}

func ProviderChannelPickScope(ids []int, categories []string, include bool) *ChannelPickScope {
	scope := &ChannelPickScope{Include: include, IDs: slices.Clone(ids)}
	if len(categories) == 0 {
		return scope
	}
	var matched []int
	err := ApplyChannelCategorySelection(DB.Model(&Channel{}), "id", "channel_category", nil, categories, true).Pluck("id", &matched).Error
	if err != nil {
		common.SysError("failed to resolve channel categories: " + err.Error())
		return &ChannelPickScope{Include: true} // fail closed for include and exclude
	}
	scope.IDs = append(scope.IDs, matched...)
	slices.Sort(scope.IDs)
	scope.IDs = slices.Compact(scope.IDs)
	return scope
}
