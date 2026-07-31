package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type kimiRegistryResponse struct {
	APIMaster kimiRegistryProvider `json:"apimaster"`
}

func TestIsKimiCodingModelUsesCapabilitiesInsteadOfNames(t *testing.T) {
	tests := []struct {
		name      string
		modelName string
		endpoints []constant.EndpointType
		quotaType int
		want      bool
	}{
		{name: "chat model", modelName: "future-model", endpoints: []constant.EndpointType{constant.EndpointTypeOpenAI}, want: true},
		{name: "image model", modelName: "future-image", endpoints: []constant.EndpointType{constant.EndpointTypeOpenAI, constant.EndpointTypeImageGeneration}, want: false},
		{name: "fixed price task", modelName: "future-video", endpoints: []constant.EndpointType{constant.EndpointTypeOpenAI}, quotaType: 1, want: false},
		{name: "anthropic only", modelName: "future-anthropic", endpoints: []constant.EndpointType{constant.EndpointTypeAnthropic}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isKimiCodingModel(tt.modelName, tt.endpoints, model.Pricing{QuotaType: tt.quotaType})
			require.Equal(t, tt.want, got)
		})
	}
}

func TestKimiProviderRegistryUsesTokenLimitsAndWritesAccessLog(t *testing.T) {
	withSelfUseModeDisabled(t)
	withTieredBillingConfig(t, map[string]string{
		"zz-kimi-chat-model":  "tiered_expr",
		"zz-kimi-image-model": "tiered_expr",
	}, map[string]string{
		"zz-kimi-chat-model":  `tier("base", p + c)`,
		"zz-kimi-image-model": `tier("base", p + c)`,
	})

	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.RegistryAccessLog{}))
	require.NoError(t, db.Create(&model.User{
		Id:       2001,
		Username: "kimi-registry-user",
		Password: "password",
		Group:    "default",
		Status:   common.UserStatusEnabled,
	}).Error)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "zz-kimi-chat-model", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "zz-kimi-image-model", ChannelId: 1, Enabled: true},
	}).Error)
	require.NoError(t, db.Create(&[]model.Model{
		{ModelName: "zz-kimi-chat-model", Status: 1, Endpoints: `{"openai":"/v1/chat/completions"}`},
		{ModelName: "zz-kimi-image-model", Status: 1, Endpoints: `{"openai":"/v1/chat/completions","image-generation":"/v1/images/generations"}`},
	}).Error)
	model.InvalidatePricingCache()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/kimi/registry.json", nil)
	ctx.Request.Header.Set("User-Agent", "kimi-code-cli/test")
	ctx.Set("id", 2001)
	ctx.Set("token_id", 321)
	common.SetContextKey(ctx, constant.ContextKeyTokenModelLimitEnabled, true)
	common.SetContextKey(ctx, constant.ContextKeyTokenModelLimit, map[string]bool{
		"zz-kimi-chat-model":  true,
		"zz-kimi-image-model": true,
	})

	KimiProviderRegistry(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "private, max-age=300", recorder.Header().Get("Cache-Control"))
	var payload kimiRegistryResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, "apimaster", payload.APIMaster.ID)
	require.Equal(t, "openai", payload.APIMaster.Type)
	require.Equal(t, "https://apimaster.ai/v1", payload.APIMaster.API)
	require.Contains(t, payload.APIMaster.Models, "zz-kimi-chat-model")
	require.NotContains(t, payload.APIMaster.Models, "zz-kimi-image-model")
	require.True(t, payload.APIMaster.Models["zz-kimi-chat-model"].ToolCall)
	require.True(t, payload.APIMaster.Models["zz-kimi-chat-model"].Reasoning)

	var accessLog model.RegistryAccessLog
	require.NoError(t, db.First(&accessLog).Error)
	require.Equal(t, 2001, accessLog.UserId)
	require.Equal(t, 321, accessLog.TokenId)
	require.Equal(t, 1, accessLog.ModelCount)
	require.Equal(t, "kimi-code-cli/test", accessLog.UserAgent)
}
