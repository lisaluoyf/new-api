package service

import (
	"fmt"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func NormalizeProviderSettings(policies []dto.ProviderSetting) ([]dto.ProviderSetting, error) {
	if len(policies) > 128 {
		return nil, fmt.Errorf("Too many provider rules")
	}
	result := make([]dto.ProviderSetting, 0, len(policies))
	seen := make(map[string]bool)
	for _, policy := range policies {
		policy.Model = strings.TrimSpace(policy.Model)
		if policy.Model == "" || len(policy.Model) > 200 || IsFreeModel(policy.Model) {
			return nil, fmt.Errorf("Invalid provider rule model")
		}
		if seen[policy.Model] {
			return nil, fmt.Errorf("Model ID must be unique")
		}
		seen[policy.Model] = true
		if policy.Mode != "include" && policy.Mode != "exclude" {
			return nil, fmt.Errorf("Invalid provider rule mode")
		}
		if len(policy.ChannelIDs) == 0 || len(policy.ChannelIDs) > 512 {
			return nil, fmt.Errorf("Select at least one channel")
		}
		policy.ChannelIDs = slices.Clone(policy.ChannelIDs)
		for _, channelID := range policy.ChannelIDs {
			if channelID <= 0 {
				return nil, fmt.Errorf("Invalid channel ID")
			}
		}
		slices.Sort(policy.ChannelIDs)
		policy.ChannelIDs = slices.Compact(policy.ChannelIDs)
		result = append(result, policy)
	}
	return result, nil
}

func ProviderPolicyForModel(c *gin.Context, modelName string) *dto.ProviderSetting {
	if c == nil || IsFreeModel(modelName) {
		return nil
	}
	settings, ok := common.GetContextKeyType[dto.UserSetting](c, constant.ContextKeyUserSetting)
	if !ok {
		return nil
	}
	if original := c.GetString("provider_settings_request_model"); original != "" {
		modelName = original
	}
	for _, policy := range settings.ProviderSettings {
		if policy.Enabled && policy.Model == modelName && (policy.Mode == "include" || policy.Mode == "exclude") {
			return &policy
		}
	}
	return nil
}

func ProviderChannelAllowed(policy *dto.ProviderSetting, channelID int) bool {
	if policy == nil {
		return true
	}
	found := slices.Contains(policy.ChannelIDs, channelID)
	if policy.Mode == "include" {
		return found
	}
	return !found
}

func ProviderChannelPickFilter(c *gin.Context, modelName string) model.ChannelPickFilter {
	policy := ProviderPolicyForModel(c, modelName)
	if policy == nil {
		return nil
	}
	return func(channel *model.Channel) bool {
		return channel != nil && ProviderChannelAllowed(policy, channel.Id)
	}
}
