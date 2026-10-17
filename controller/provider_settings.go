package controller

import (
	"encoding/json"
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
	Rules          []dto.ProviderSetting `json:"rules"`
	categoryFields []bool
}

// Track field presence so older clients can omit saved categories without
// clearing them. Explicit null and [] are still changes to an existing rule.
func (request *providerSettingsRequest) UnmarshalJSON(data []byte) error {
	type plain providerSettingsRequest
	var parsed plain
	if err := common.Unmarshal(data, &parsed); err != nil {
		return err
	}
	var raw struct {
		Rules []map[string]json.RawMessage `json:"rules"`
	}
	if err := common.Unmarshal(data, &raw); err != nil {
		return err
	}
	*request = providerSettingsRequest(parsed)
	request.categoryFields = make([]bool, len(raw.Rules))
	for index, rule := range raw.Rules {
		_, request.categoryFields[index] = rule["channel_categories"]
	}
	return nil
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
	c.Header("Cache-Control", "private, no-store")
	user, err := model.GetUserById(c.GetInt("id"), false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	rules := user.GetSetting().ProviderSettings
	if rules == nil {
		rules = []dto.ProviderSetting{}
	}
	common.ApiSuccess(c, gin.H{"rules": rules, "models": providerSettingModels(user), "channel_categories_enabled": model.ChannelCategoriesEnabled()})
}

func SaveProviderSettings(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 512*1024)
	var request providerSettingsRequest
	if err := c.ShouldBindJSON(&request); err != nil || request.Rules == nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid provider settings"})
		return
	}
	user, err := model.GetUserById(c.GetInt("id"), false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	settings := user.GetSetting()
	categoriesEnabled := model.ChannelCategoriesEnabled()
	for index := range request.Rules {
		request.Rules[index].Model = strings.TrimSpace(request.Rules[index].Model)
		for _, saved := range settings.ProviderSettings {
			if saved.Model == request.Rules[index].Model && !request.categoryFields[index] {
				request.Rules[index].ChannelCategories = slices.Clone(saved.ChannelCategories)
			}
		}
	}
	rules, err := service.NormalizeProviderSettings(request.Rules)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	models := providerSettingModels(user)
	for _, rule := range rules {
		var previous *dto.ProviderSetting
		for _, saved := range settings.ProviderSettings {
			if saved.Model == rule.Model {
				previous = &saved
				break
			}
		}
		if !categoriesEnabled && (len(rule.ChannelCategories) > 0 || (previous != nil && len(previous.ChannelCategories) > 0)) {
			unchanged := false
			if previous != nil {
				normalized, normalizeErr := service.NormalizeProviderSettings([]dto.ProviderSetting{*previous})
				unchanged = normalizeErr == nil && rule.Mode == normalized[0].Mode && slices.Equal(rule.ChannelIDs, normalized[0].ChannelIDs) && slices.Equal(rule.ChannelCategories, normalized[0].ChannelCategories)
			}
			if !unchanged {
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Channel category selection is disabled"})
				return
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
	c.Header("Cache-Control", "private, no-store")
	common.ApiSuccess(c, gin.H{"rules": rules, "channel_categories_enabled": categoriesEnabled})
}

func GetProviderSettingChannels(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
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
		item := publicMarketplacePriceItem(name, row)
		if !model.ChannelCategoriesEnabled() {
			item.ChannelCategory = ""
		}
		items = append(items, item)
	}
	sortPublicMarketplaceItems(items)
	common.ApiSuccess(c, items)
}
