package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPublicMarketplaceHidesFreeModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/public/marketplace?model="+service.FreeModelID, nil)
	GetPublicMarketplace(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool  `json:"success"`
		Data    []any `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Empty(t, response.Data)
}

func TestSaveFreeModelRoutePriceDoesNotOverwriteMappedUpstreamPrice(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.ChannelModelPricing{}))

	mapping := `{"apimaster-freemodel":"provider/real-free"}`
	channel := model.Channel{Name: "free-route", Key: "test", Models: service.FreeModelID + ",provider/real-free", ModelMapping: &mapping, Status: common.ChannelStatusEnabled}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, db.Create(&model.ChannelModelPricing{ChannelId: channel.Id, ModelName: "provider/old-free", InputPrice: 1, PricingSource: "free_model"}).Error)
	require.NoError(t, db.Create(&model.ChannelModelPricing{
		ChannelId:          channel.Id,
		ModelName:          "provider/real-free",
		InputPrice:         1,
		OutputPrice:        4,
		CachePrice:         0.1,
		CacheCreationPrice: 1.25,
		GroupRatio:         0.95,
		Currency:           "USD",
		PricingSource:      "api",
		BillingMode:        "tiered",
		BillingExpr:        `{"tiers":[{"up_to":1000,"input_price":1}]}`,
	}).Error)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/admin/free-model/channels/1/route-price", strings.NewReader(`{"input_price":0.0001}`))
	ctx.Params = gin.Params{{Key: "channel_id", Value: "1"}}
	SaveFreeModelRoutePrice(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var routeRow model.ChannelModelPricing
	require.NoError(t, db.Where("channel_id = ? AND model_name = ?", channel.Id, service.FreeModelID).First(&routeRow).Error)
	require.InDelta(t, 0.0001, routeRow.InputPrice, 0.00000001)
	require.Equal(t, "free_model", routeRow.PricingSource)

	var upstreamRow model.ChannelModelPricing
	require.NoError(t, db.Where("channel_id = ? AND model_name = ?", channel.Id, "provider/real-free").First(&upstreamRow).Error)
	require.InDelta(t, 1, upstreamRow.InputPrice, 0.00000001)
	require.InDelta(t, 4, upstreamRow.OutputPrice, 0.00000001)
	require.InDelta(t, 0.1, upstreamRow.CachePrice, 0.00000001)
	require.InDelta(t, 1.25, upstreamRow.CacheCreationPrice, 0.00000001)
	require.InDelta(t, 0.95, upstreamRow.GroupRatio, 0.00000001)
	require.Equal(t, "api", upstreamRow.PricingSource)
	require.Equal(t, "tiered", upstreamRow.BillingMode)
	require.NotEmpty(t, upstreamRow.BillingExpr)

	normalPrice, ok := service.ChannelModelPriceData(channel.Id, "provider/real-free")
	require.True(t, ok)
	require.InDelta(t, 0.5, normalPrice.ModelRatio, 0.00000001)
	require.InDelta(t, 4, normalPrice.CompletionRatio, 0.00000001)
	require.InDelta(t, 0.1, normalPrice.CacheRatio, 0.00000001)
	require.InDelta(t, 1.25, normalPrice.CacheCreationRatio, 0.00000001)
	var staleCount int64
	require.NoError(t, db.Model(&model.ChannelModelPricing{}).Where("channel_id = ? AND model_name = ?", channel.Id, "provider/old-free").Count(&staleCount).Error)
	require.Zero(t, staleCount)
}

func TestSaveFreeModelMemberConfigPersistsCapabilities(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.FreeModelMember{}))
	mapping := `{"apimaster-freemodel":"provider/free"}`
	channel := model.Channel{Name: "free", Key: "secret", Models: service.FreeModelID, ModelMapping: &mapping, Status: common.ChannelStatusEnabled}
	require.NoError(t, db.Create(&channel).Error)
	body := `{"enabled":false,"priority":0,"weight":25,"codex_priority":300,"codex_weight":7,"capabilities":{"text":true,"vision":true,"tools":false,"codex_tools":true,"required_tool_call":false,"json_object":true,"json_schema":false},"endpoints":{"chat_completions":true,"responses":false,"messages":true},"max_context_tokens":65536,"timeout_ms":12000}`
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/admin/free-model/channels/1/config", strings.NewReader(body))
	ctx.Params = gin.Params{{Key: "channel_id", Value: "1"}}
	SaveFreeModelMember(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)
	stored, exists, err := model.GetFreeModelMember(channel.Id)
	require.NoError(t, err)
	require.True(t, exists)
	require.False(t, stored.Enabled)
	require.Zero(t, stored.Priority)
	require.Equal(t, uint(25), stored.Weight)
	require.Equal(t, int64(300), *stored.CodexPriority)
	require.Equal(t, uint(7), *stored.CodexWeight)
	require.True(t, stored.Vision)
	require.False(t, stored.Tools)
	require.True(t, *stored.CodexTools)
	require.False(t, stored.SupportsRequiredToolCall())
	require.True(t, stored.SupportsChatCompletions())
	require.False(t, stored.SupportsResponses())
	require.True(t, stored.SupportsMessages())
}
