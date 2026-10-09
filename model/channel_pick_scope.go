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
	for _, scope := range scopes {
		if scope == nil {
			continue
		}
		if scope.Include {
			if len(scope.IDs) == 0 {
				query = query.Where("1 = 0")
			} else {
				query = query.Where("channel_id IN ?", scope.IDs)
			}
		} else if len(scope.IDs) > 0 {
			query = query.Where("channel_id NOT IN ?", scope.IDs)
		}
	}
	return query
}
