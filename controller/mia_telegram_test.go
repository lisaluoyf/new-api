package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupMiaTelegramTestRouter(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	initModelListColumnNames(t)
	require.NoError(t, SetModelTabsJSON([]byte(`[
		{"model_id":"grok-4.5","label":"Grok 4.5","accent":"#94a3b8"},
		{"model_id":"gemini-2.5-flash-image","label":"Nano Banana","accent":"#4285f4"},
		{"model_id":"gemini-3-pro-image","label":"Nano Banana Pro","accent":"#4285f4"},
		{"model_id":"gemini-3.1-flash-image","label":"Nano Banana 2","accent":"#4285f4"},
		{"model_id":"gpt-image-2","label":"Image 2","accent":"#22d3ee"},
		{"model_id":"kling-v3-omni","label":"Kling V3 Omni","accent":"#f97316"},
		{"model_id":"minimax-h3","label":"MiniMax-H3","accent":"#f97316"}
	]`)))
	gin.SetMode(gin.TestMode)
	common.RedisEnabled = false
	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false

	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.Token{},
		&model.Channel{},
		&model.Ability{},
		&model.ChannelModelPricing{},
		&model.Model{},
		&model.Vendor{},
	))
	model.DB = db
	model.LOG_DB = db
	model.InvalidatePricingCache()

	previousSecret := common.MiaInternalServiceKey
	previousSelfUseMode := operation_setting.SelfUseModeEnabled
	common.MiaInternalServiceKey = "test-mia-internal-secret"
	operation_setting.SelfUseModeEnabled = true
	t.Cleanup(func() {
		common.MiaInternalServiceKey = previousSecret
		operation_setting.SelfUseModeEnabled = previousSelfUseMode
		model.InvalidatePricingCache()
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	router := gin.New()
	router.POST(
		"/api/user/internal/telegram-api-key",
		middleware.RequireMiaInternalService(),
		ResolveMiaTelegramAPIKey,
	)
	router.POST(
		"/api/user/internal/mia-models",
		middleware.RequireMiaInternalService(),
		GetMiaTelegramModelCatalog,
	)
	router.POST(
		"/api/user/internal/mia-debug-identities",
		middleware.RequireMiaInternalService(),
		ResolveMiaDebugIdentities,
	)
	return router, db
}

func performMiaTelegramRequest(router *gin.Engine, secret string, body string) *httptest.ResponseRecorder {
	return performMiaInternalRequest(router, "/api/user/internal/telegram-api-key", secret, body)
}

func performMiaInternalRequest(router *gin.Engine, path, secret, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	if secret != "" {
		request.Header.Set("X-Mia-Internal-Key", secret)
	}
	router.ServeHTTP(recorder, request)
	return recorder
}

type miaModelCatalogTestResponse struct {
	Success bool `json:"success"`
	Data    struct {
		UserID int                   `json:"user_id"`
		Models []miaModelCatalogItem `json:"models"`
	} `json:"data"`
}

func TestResolveMiaTelegramAPIKeyRequiresDedicatedAuthentication(t *testing.T) {
	router, _ := setupMiaTelegramTestRouter(t)
	recorder := performMiaTelegramRequest(router, "", `{"telegram_user_id":"123456"}`)
	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "test-mia-internal-secret")
}

func TestResolveMiaDebugIdentitiesReturnsOnlyEnabledBoundUsers(t *testing.T) {
	router, db := setupMiaTelegramTestRouter(t)
	users := []model.User{
		{Username: "lisa-debug", Email: "Lisa.Luoyf@gmail.com", Status: common.UserStatusEnabled, TelegramId: "7553714675", AffCode: "dbg1"},
		{Username: "unbound-debug", Email: "unbound@example.com", Status: common.UserStatusEnabled, AffCode: "dbg2"},
		{Username: "disabled-debug", Email: "disabled@example.com", Status: common.UserStatusDisabled, TelegramId: "123456", AffCode: "dbg3"},
	}
	for index := range users {
		require.NoError(t, db.Create(&users[index]).Error)
	}

	unauthorized := performMiaInternalRequest(router, "/api/user/internal/mia-debug-identities", "", `{"emails":["lisa.luoyf@gmail.com"]}`)
	require.Equal(t, http.StatusUnauthorized, unauthorized.Code)

	response := performMiaInternalRequest(router, "/api/user/internal/mia-debug-identities", "test-mia-internal-secret", `{"emails":["lisa.luoyf@gmail.com","unbound@example.com","disabled@example.com"]}`)
	require.Equal(t, http.StatusOK, response.Code)
	require.JSONEq(t, `{"success":true,"data":{"identities":[{"email":"lisa.luoyf@gmail.com","telegram_user_id":"7553714675"}]}}`, response.Body.String())
}

func TestResolveMiaDebugIdentitiesRejectsInvalidInput(t *testing.T) {
	router, _ := setupMiaTelegramTestRouter(t)
	response := performMiaInternalRequest(router, "/api/user/internal/mia-debug-identities", "test-mia-internal-secret", `{"emails":["not-an-email"]}`)
	require.Equal(t, http.StatusBadRequest, response.Code)
}

func TestResolveMiaTelegramAPIKeyReturnsUsableBoundUserToken(t *testing.T) {
	router, db := setupMiaTelegramTestRouter(t)
	user := model.User{
		Username:   "mia-test-user",
		Password:   "unused-test-password",
		Status:     common.UserStatusEnabled,
		Role:       common.RoleCommonUser,
		Group:      "default",
		TelegramId: "123456",
	}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&model.Channel{Id: 1, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled}).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "grok-4.5", ChannelId: 1, Enabled: true}).Error)

	restrictedIPs := "203.0.113.10"
	tokens := []model.Token{
		{UserId: user.Id, Key: "expired-key", Name: "expired", Status: common.TokenStatusEnabled, ExpiredTime: 1, UnlimitedQuota: true},
		{UserId: user.Id, Key: "wrong-model-key", Name: "limited", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true, ModelLimitsEnabled: true, ModelLimits: "gpt-5"},
		{UserId: user.Id, Key: "ip-restricted-key", Name: "ip", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true, AllowIps: &restrictedIPs},
		{UserId: user.Id, Key: "usable-key", Name: "usable", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true, ModelLimitsEnabled: true, ModelLimits: "grok-4.5"},
	}
	for i := range tokens {
		require.NoError(t, db.Create(&tokens[i]).Error)
	}

	recorder := performMiaTelegramRequest(router, "test-mia-internal-secret", `{"telegram_user_id":"123456","model":"grok-4.5"}`)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.JSONEq(t, `{"success":true,"data":{"user_id":1,"token_id":4,"api_key":"usable-key"}}`, recorder.Body.String())
}

func TestResolveMiaTelegramAPIKeySelectsTokenWhoseGroupCanRouteModel(t *testing.T) {
	router, db := setupMiaTelegramTestRouter(t)
	user := model.User{
		Username:   "mia-group-token-user",
		Password:   "unused-test-password",
		Status:     common.UserStatusEnabled,
		Role:       common.RoleCommonUser,
		Group:      "default",
		TelegramId: "234567",
	}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&model.Channel{Id: 1, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled}).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "grok-4.5", ChannelId: 1, Enabled: true}).Error)
	tokens := []model.Token{
		{UserId: user.Id, Key: "wrong-group-key", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true, Group: "other"},
		{UserId: user.Id, Key: "right-group-key", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true, Group: "default"},
	}
	for i := range tokens {
		require.NoError(t, db.Create(&tokens[i]).Error)
	}

	recorder := performMiaTelegramRequest(router, "test-mia-internal-secret", `{"telegram_user_id":"234567","model":"grok-4.5"}`)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "right-group-key")
	require.NotContains(t, recorder.Body.String(), "wrong-group-key")
}

func TestResolveMiaTelegramAPIKeyReportsUnboundAndMissingKey(t *testing.T) {
	router, db := setupMiaTelegramTestRouter(t)

	unbound := performMiaTelegramRequest(router, "test-mia-internal-secret", `{"telegram_user_id":"999999"}`)
	require.Equal(t, http.StatusNotFound, unbound.Code)
	require.Contains(t, unbound.Body.String(), "telegram_not_bound")

	user := model.User{
		Username:   "mia-no-key",
		Password:   "unused-test-password",
		Status:     common.UserStatusEnabled,
		Role:       common.RoleCommonUser,
		TelegramId: "888888",
	}
	require.NoError(t, db.Create(&user).Error)
	missingKey := performMiaTelegramRequest(router, "test-mia-internal-secret", `{"telegram_user_id":"888888"}`)
	require.Equal(t, http.StatusNotFound, missingKey.Code)
	require.Contains(t, missingKey.Body.String(), "no_usable_api_key")
}

func TestGetMiaTelegramModelCatalogUnionsUsableTokensAndClassifiesEndpoints(t *testing.T) {
	router, db := setupMiaTelegramTestRouter(t)
	user := model.User{
		Username:   "mia-catalog-user",
		Password:   "unused-test-password",
		Status:     common.UserStatusEnabled,
		Role:       common.RoleCommonUser,
		Group:      "default",
		TelegramId: "1234567",
	}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&model.Channel{Id: 1, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled}).Error)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "grok-4.5", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "vision-pro-no-tag", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "gemini-2.5-flash-image", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "gemini-3-pro-image", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "gemini-3.1-flash-image", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "gpt-image-2", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "MiniMax-H3", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "kling-v3-omni", ChannelId: 1, Enabled: true},
		{Group: "other", Model: "grok-other-group", ChannelId: 1, Enabled: true},
	}).Error)
	require.NoError(t, db.Create(&[]model.Model{
		{ModelName: "grok-4.5", Tags: "chat, Vision | vision-recommended;recommended", Status: 1},
		{ModelName: "vision-pro-no-tag", Tags: "chat", Status: 1},
	}).Error)

	restrictedIPs := "203.0.113.10"
	tokens := []model.Token{
		{UserId: user.Id, Key: "disabled-secret", Status: common.TokenStatusDisabled, ExpiredTime: -1, UnlimitedQuota: true},
		{UserId: user.Id, Key: "expired-secret", Status: common.TokenStatusEnabled, ExpiredTime: 1, UnlimitedQuota: true},
		{UserId: user.Id, Key: "empty-secret", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 0},
		{UserId: user.Id, Key: "ip-secret", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true, AllowIps: &restrictedIPs},
		{UserId: user.Id, Key: "usable-secret-one", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true, ModelLimitsEnabled: true, ModelLimits: "grok-4.5,vision-pro-no-tag,gemini-2.5-flash-image,gemini-3-pro-image,gemini-3.1-flash-image,gpt-image-2,kling-v3-omni,grok-other-group"},
		{UserId: user.Id, Key: "usable-secret-two", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true, ModelLimitsEnabled: true, ModelLimits: "grok-4.5,minimax-h3"},
	}
	for i := range tokens {
		require.NoError(t, db.Create(&tokens[i]).Error)
	}
	model.InvalidatePricingCache()

	recorder := performMiaInternalRequest(
		router,
		"/api/user/internal/mia-models",
		"test-mia-internal-secret",
		`{"telegram_user_id":"1234567"}`,
	)
	require.Equal(t, http.StatusOK, recorder.Code)
	responseBody := recorder.Body.String()
	for _, secret := range []string{
		"disabled-secret", "expired-secret", "empty-secret", "ip-secret",
		"usable-secret-one", "usable-secret-two",
	} {
		require.NotContains(t, responseBody, secret)
	}
	require.NotContains(t, responseBody, "token_id")
	require.NotContains(t, responseBody, "api_key")

	var response miaModelCatalogTestResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Equal(t, user.Id, response.Data.UserID)
	require.Len(t, response.Data.Models, 8)

	byID := make(map[string]miaModelCatalogItem, len(response.Data.Models))
	for _, item := range response.Data.Models {
		byID[item.ID] = item
	}
	require.Equal(t, "chat", byID["grok-4.5"].Capability)
	require.True(t, byID["grok-4.5"].Recommended)
	require.True(t, byID["grok-4.5"].SupportsVision)
	require.True(t, byID["grok-4.5"].VisionRecommended)
	require.False(t, byID["vision-pro-no-tag"].SupportsVision, "vision support must not be inferred from the model name")
	require.False(t, byID["vision-pro-no-tag"].VisionRecommended)
	require.Equal(t, "Nano Banana", byID["gemini-2.5-flash-image"].DisplayName)
	require.Equal(t, "vertex-ai", byID["gemini-2.5-flash-image"].Vendor)
	require.Equal(t, "Nano Banana Pro", byID["gemini-3-pro-image"].DisplayName)
	require.Equal(t, "Nano Banana 2", byID["gemini-3.1-flash-image"].DisplayName)
	require.Equal(t, "image", byID["gpt-image-2"].Capability)
	require.Equal(t, "Image 2", byID["gpt-image-2"].DisplayName)
	require.False(t, byID["gpt-image-2"].SupportsVision)
	require.Contains(t, byID["gpt-image-2"].SupportedEndpointTypes, constant.EndpointTypeImageGeneration)
	require.Equal(t, "video", byID["MiniMax-H3"].Capability)
	require.Equal(t, "MiniMax-H3", byID["MiniMax-H3"].DisplayName)
	require.False(t, byID["MiniMax-H3"].Recommended)
	require.NotNil(t, byID["MiniMax-H3"].VideoCapabilities)
	require.Equal(t, []string{"text_to_video", "image_to_video"}, byID["MiniMax-H3"].VideoCapabilities.Modes)
	require.Equal(t, miaIntegerRange{Min: 4, Max: 15, Default: 4}, byID["MiniMax-H3"].VideoCapabilities.DurationSeconds)
	require.Equal(t, []string{"720p"}, byID["MiniMax-H3"].VideoCapabilities.Resolutions)
	require.Equal(t, "720p", byID["MiniMax-H3"].VideoCapabilities.DefaultResolution)
	require.Equal(t, []string{"1:1", "16:9", "9:16"}, byID["MiniMax-H3"].VideoCapabilities.AspectRatios)
	require.Equal(t, "16:9", byID["MiniMax-H3"].VideoCapabilities.DefaultAspectRatio)
	require.Equal(t, 10, byID["MiniMax-H3"].VideoCapabilities.MaxReferenceImages)
	require.Contains(t, byID["MiniMax-H3"].SupportedEndpointTypes, constant.EndpointTypeOpenAIVideo)
	require.Equal(t, "video", byID["kling-v3-omni"].Capability)
	require.False(t, byID["kling-v3-omni"].Recommended)
	require.NotNil(t, byID["kling-v3-omni"].VideoCapabilities)
	require.NotContains(t, byID, "grok-other-group")

	resolver := performMiaTelegramRequest(
		router,
		"test-mia-internal-secret",
		`{"telegram_user_id":"1234567","model":"minimax-h3"}`,
	)
	require.Equal(t, http.StatusOK, resolver.Code)
	require.Contains(t, resolver.Body.String(), "usable-secret-two")
}

func TestGetMiaTelegramModelCatalogRequiresAuthenticationAndUsableKey(t *testing.T) {
	router, db := setupMiaTelegramTestRouter(t)
	unauthorized := performMiaInternalRequest(
		router,
		"/api/user/internal/mia-models",
		"",
		`{"telegram_user_id":"7654321"}`,
	)
	require.Equal(t, http.StatusUnauthorized, unauthorized.Code)

	user := model.User{
		Username:   "mia-empty-catalog-user",
		Password:   "unused-test-password",
		Status:     common.UserStatusEnabled,
		Group:      "default",
		TelegramId: "7654321",
	}
	require.NoError(t, db.Create(&user).Error)
	missingKey := performMiaInternalRequest(
		router,
		"/api/user/internal/mia-models",
		"test-mia-internal-secret",
		`{"telegram_user_id":"7654321"}`,
	)
	require.Equal(t, http.StatusNotFound, missingKey.Code)
	require.Contains(t, missingKey.Body.String(), "no_usable_api_key")
}

func TestGetMiaTelegramModelCatalogUsesChannelPriceForVideoWithoutTierTable(t *testing.T) {
	router, db := setupMiaTelegramTestRouter(t)
	rechargeRate := 1.25
	apimasterPriceRatio := 1.2
	exclusiveSetting := `{"client_exclusive":"codex"}`
	user := model.User{
		Username: "mia-video-price-user", Password: "unused-test-password",
		Status: common.UserStatusEnabled, Role: common.RoleCommonUser,
		Group: "default", TelegramId: "4567890",
	}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&model.Channel{
		Id: 1, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Models: "sora-2",
		RechargeRate: &rechargeRate, ApimasterPriceRatio: &apimasterPriceRatio,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group: "default", Model: "sora-2", ChannelId: 1, Enabled: true,
	}).Error)
	// sora-2 intentionally has no VideoModelPricing entry. Its direct channel
	// price is the source used by normal routing and must be exposed to Mia too.
	require.NoError(t, db.Create(&model.ChannelModelPricing{
		ChannelId: 1, ModelName: "sora-2", InputPrice: 0.08, GroupRatio: 1,
	}).Error)
	// It is cheaper, but the marketplace marks it Codex-only. Mia must not use
	// this card as the lowest price because Telegram users cannot route through it.
	require.NoError(t, db.Create(&model.Channel{
		Id: 2, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled,
		Models: "sora-2", Setting: &exclusiveSetting,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group: "default", Model: "sora-2", ChannelId: 2, Enabled: true,
	}).Error)
	require.NoError(t, db.Create(&model.ChannelModelPricing{
		ChannelId: 2, ModelName: "sora-2", InputPrice: 0.01, GroupRatio: 1,
	}).Error)
	require.NoError(t, db.Create(&model.Token{
		UserId: user.Id, Key: "usable-video-price-key", Status: common.TokenStatusEnabled,
		ExpiredTime: -1, UnlimitedQuota: true,
	}).Error)
	model.InvalidatePricingCache()

	recorder := performMiaInternalRequest(
		router,
		"/api/user/internal/mia-models",
		"test-mia-internal-secret",
		`{"telegram_user_id":"4567890"}`,
	)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response miaModelCatalogTestResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.Len(t, response.Data.Models, 1)
	pricing := response.Data.Models[0].Pricing
	require.NotNil(t, pricing, recorder.Body.String())
	require.Equal(t, "video", response.Data.Models[0].Capability)
	require.Equal(t, "second", pricing.Unit)
	require.NotNil(t, pricing.Price)
	require.InDelta(t, 0.12, *pricing.Price, 1e-9)
	// Models without a media tier table still inherit the same unified official
	// price used by marketplace cards when one is configured.
	require.NotNil(t, pricing.DiscountRatio)
	require.Greater(t, *pricing.DiscountRatio, 0.0)
	require.Less(t, *pricing.DiscountRatio, 1.0)
}

func TestMiaModelCapabilityIncludesResponseChatModels(t *testing.T) {
	capability, ok := miaModelCapability([]constant.EndpointType{constant.EndpointTypeOpenAIResponse})
	require.True(t, ok)
	require.Equal(t, "chat", capability)
}

func TestMiaCatalogNormalizesOnlyProviderHaikuAliases(t *testing.T) {
	require.Equal(t, "claude-haiku-4-5", miaCatalogModelID("claude-haiku-4-5-20251001"))
	require.Equal(t, "claude-haiku-4-5", miaCatalogModelID("anthropic/claude-haiku-4.5"))
	// Nano Banana IDs are distinct public products, not aliases of one another.
	require.Equal(t, "gemini-3.1-flash-image-preview", miaCatalogModelID("gemini-3.1-flash-image-preview"))
	require.Equal(t, "gemini-2.5-flash-image", miaCatalogModelID("gemini-2.5-flash-image"))
}

func TestMiaCatalogUsesBackendModelTabLabels(t *testing.T) {
	originalJSON := append([]byte(nil), catalogModelTabsJSON...)
	originalLabels := catalogModelTabLabels
	t.Cleanup(func() {
		catalogModelTabsJSON = originalJSON
		catalogModelTabLabels = originalLabels
	})
	require.NoError(t, SetModelTabsJSON([]byte(`[
		{"model_id":"gemini-3.1-flash-image","label":"Nano Banana 2","accent":"#4285f4"},
		{"model_id":"gpt-image-2","label":"Image 2","accent":"#22d3ee"}
	]`)))
	require.Equal(t, "Nano Banana 2", catalogModelTabLabel("gemini-3.1-flash-image"))
	require.Equal(t, "Image 2", catalogModelTabLabel("GPT-IMAGE-2"))
	require.Equal(t, "future-model", catalogModelTabLabel(" future-model "))
}

func TestMiaModelVisionTagsAreExplicitAndTokenized(t *testing.T) {
	require.True(t, miaModelHasTag("chat, vision;vision-recommended", "vision"))
	require.True(t, miaModelHasTag("chat, vision;vision-recommended", "vision-recommended"))
	require.True(t, miaModelHasTag("VISION", "vision"))
	require.False(t, miaModelHasTag("computer-visionary", "vision"))
	require.False(t, miaModelHasTag("vision-recommended", "vision"))
}

func TestMiaVideoCapabilitiesCoverVideoCatalog(t *testing.T) {
	h3 := miaVideoModelCapabilities("video")
	require.NotNil(t, h3)
	require.Equal(t, 4, h3.DurationSeconds.Default)
	require.Equal(t, 10, h3.MaxReferenceImages)

	require.Nil(t, miaVideoModelCapabilities("chat"))
}
