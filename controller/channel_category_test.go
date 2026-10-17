package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMarketplaceCategoryVisibilityDoesNotMutateCache(t *testing.T) {
	setupUserAffRatioOverrideControllerTestDB(t)
	require.NoError(t, model.DB.Create(&model.User{Id: 81, Username: "category-admin", AffCode: "category-admin", Role: common.RoleAdminUser}).Error)
	require.NoError(t, model.DB.Create(&model.User{Id: 82, Username: "category-user", AffCode: "category-user", Role: common.RoleCommonUser}).Error)
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	old, exists := common.OptionMap[model.ChannelCategoriesPublicEnabledOption]
	common.OptionMap[model.ChannelCategoriesPublicEnabledOption] = "false"
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if exists {
			common.OptionMap[model.ChannelCategoriesPublicEnabledOption] = old
		} else {
			delete(common.OptionMap, model.ChannelCategoriesPublicEnabledOption)
		}
	})
	items := []PublicMarketplaceItem{{ChannelID: 1, ChannelCategory: "official"}}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.Empty(t, marketplaceItemsForViewer(ctx, items)[0].ChannelCategory)
	ctx.Set("id", 82)
	require.Empty(t, marketplaceItemsForViewer(ctx, items)[0].ChannelCategory)
	ctx.Set("id", 81)
	require.Equal(t, "official", marketplaceItemsForViewer(ctx, items)[0].ChannelCategory)
	require.Equal(t, "official", items[0].ChannelCategory)
	common.OptionMapRWMutex.Lock()
	common.OptionMap[model.ChannelCategoriesPublicEnabledOption] = "true"
	common.OptionMapRWMutex.Unlock()
	ctx.Set("id", 0)
	require.Equal(t, "official", marketplaceItemsForViewer(ctx, items)[0].ChannelCategory)
}
