package model

import (
	"slices"

	"gorm.io/gorm"
)

type ChannelPickScope struct {
	Include bool
	IDs     []int
}

func (scope *ChannelPickScope) Contains(channelID int) bool {
	if scope == nil {
		return true
	}
	found := slices.Contains(scope.IDs, channelID)
	return found == scope.Include
}

func applyChannelPickScope(query *gorm.DB, scopes []*ChannelPickScope) *gorm.DB {
	if len(scopes) == 0 || scopes[0] == nil {
		return query
	}
	scope := scopes[0]
	if scope.Include {
		if len(scope.IDs) == 0 {
			return query.Where("1 = 0")
		}
		return query.Where("channel_id IN ?", scope.IDs)
	}
	if len(scope.IDs) > 0 {
		return query.Where("channel_id NOT IN ?", scope.IDs)
	}
	return query
}
