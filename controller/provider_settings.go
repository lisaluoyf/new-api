package controller

import (
	"net/http"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

type providerSettingsRequest struct {
	Rules []dto.ProviderSetting `json:"rules"`
}

func providerSettingModels(user *model.User) []string {
	models := make([]string, 0)
	for group := range service.GetUserUsableGroups(user.Group) {
		for _, name := range model.GetGroupEnabledModels(group) {
			if !service.IsFreeModel(name) && !slices.Contains(models, name) {
				models = append(models, name)
			}
		}
	}
	slices.Sort(models)
	return models
}

func GetProviderSettings(c *gin.Context) {
	user, err := model.GetUserById(c.GetInt("id"), false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	rules := user.GetSetting().ProviderSettings
	if rules == nil {
		rules = []dto.ProviderSetting{}
	}
	common.ApiSuccess(c, gin.H{"rules": rules, "models": providerSettingModels(user)})
}

func SaveProviderSettings(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 512*1024)
	var request providerSettingsRequest
	if err := c.ShouldBindJSON(&request); err != nil || request.Rules == nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid provider settings"})
		return
	}
	rules, err := service.NormalizeProviderSettings(request.Rules)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	user, err := model.GetUserById(c.GetInt("id"), false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	settings := user.GetSetting()
	models := providerSettingModels(user)
	for _, rule := range rules {
		var previous *dto.ProviderSetting
		for _, saved := range settings.ProviderSettings {
			if saved.Model == rule.Model {
				previous = &saved
				break
			}
		}
		if !slices.Contains(models, rule.Model) && previous == nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Model is not available for provider settings"})
			return
		}
		availableIDs := make(map[int]bool)
		for _, row := range publicMarketplacePricingRows(rule.Model) {
			availableIDs[row.ChannelID] = true
		}
		for _, channelID := range rule.ChannelIDs {
			if !availableIDs[channelID] && (previous == nil || !slices.Contains(previous.ChannelIDs, channelID)) {
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Channel is not available for this model"})
				return
			}
		}
	}
	settings.ProviderSettings = rules
	if err := user.UpdateSetting(settings); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"rules": rules})
}

func GetProviderSettingChannels(c *gin.Context) {
	name := strings.TrimSpace(c.Query("model"))
	user, err := model.GetUserById(c.GetInt("id"), false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !slices.Contains(providerSettingModels(user), name) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Model is not available for provider settings"})
		return
	}
	items := make([]PublicMarketplaceItem, 0)
	for _, row := range publicMarketplacePricingRows(name) {
		items = append(items, publicMarketplacePriceItem(name, row))
	}
	sortPublicMarketplaceItems(items)
	common.ApiSuccess(c, items)
}
