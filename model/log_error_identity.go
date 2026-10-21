package model

import "github.com/QuantumNous/new-api/common"

// One ownership-scoped log page may contain many rows from the same channel.
// Load routing identities once per page, before public DTOs clear channel IDs.
func logErrorIdentityValues(logs []*Log) map[int][]string {
	result := map[int][]string{}
	for _, log := range logs {
		if log != nil {
			result[log.ChannelId] = append(result[log.ChannelId], common.UpstreamIdentityValues(log.ChannelName, "", "", "")...)
		}
	}
	if DB == nil || len(logs) == 0 || !DB.Migrator().HasTable(&Channel{}) {
		return result
	}
	ids := []int{}
	seen := map[int]bool{}
	for _, log := range logs {
		if log != nil && log.ChannelId > 0 {
			if !seen[log.ChannelId] {
				ids = append(ids, log.ChannelId)
				seen[log.ChannelId] = true
			}
		}
	}
	if len(ids) == 0 {
		return result
	}
	var channels []Channel
	if DB.Select("id", "name", "key", "type", "base_url").Where("id IN ?", ids).Find(&channels).Error != nil {
		return result
	}
	for _, channel := range channels {
		values := common.UpstreamIdentityValues(channel.Name, channel.GetBaseURL(), "", "")
		values = append(values, channel.Key)
		result[channel.Id] = append(result[channel.Id], values...)
	}
	return result
}
