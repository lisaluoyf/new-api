package controller

import (
	"slices"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// Never mutate the shared marketplace cache while stripping viewer-only fields.
func marketplaceItemsForViewer(c *gin.Context, items []PublicMarketplaceItem) []PublicMarketplaceItem {
	if model.ChannelCategoriesPublicEnabled() || (c.GetInt("id") > 0 && model.IsAdmin(c.GetInt("id"))) {
		return items
	}
	result := slices.Clone(items)
	for index := range result {
		result[index].ChannelCategory = ""
	}
	return result
}
