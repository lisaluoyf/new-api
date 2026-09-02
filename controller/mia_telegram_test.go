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
		{Group: "default", Model: "gpt-image-2", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "minimax-h3", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "kling-v3-omni", ChannelId: 1, Enabled: true},
		{Group: "other", Model: "grok-other-group", ChannelId: 1, Enabled: true},
	}).Error)

	restrictedIPs := "203.0.113.10"
	tokens := []model.Token{
		{UserId: user.Id, Key: "disabled-secret", Status: common.TokenStatusDisabled, ExpiredTime: -1, UnlimitedQuota: true},
		{UserId: user.Id, Key: "expired-secret", Status: common.TokenStatusEnabled, ExpiredTime: 1, UnlimitedQuota: true},
		{UserId: user.Id, Key: "empty-secret", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 0},
		{UserId: user.Id, Key: "ip-secret", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true, AllowIps: &restrictedIPs},
		{UserId: user.Id, Key: "usable-secret-one", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true, ModelLimitsEnabled: true, ModelLimits: "grok-4.5,gpt-image-2,kling-v3-omni,grok-other-group"},
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
	require.Len(t, response.Data.Models, 4)

	byID := make(map[string]miaModelCatalogItem, len(response.Data.Models))
	for _, item := range response.Data.Models {
		byID[item.ID] = item
	}
	require.Equal(t, "chat", byID["grok-4.5"].Capability)
	require.True(t, byID["grok-4.5"].Recommended)
	require.Equal(t, "image", byID["gpt-image-2"].Capability)
	require.Contains(t, byID["gpt-image-2"].SupportedEndpointTypes, constant.EndpointTypeImageGeneration)
	require.Equal(t, "video", byID["minimax-h3"].Capability)
	require.True(t, byID["minimax-h3"].Recommended)
	require.Contains(t, byID["minimax-h3"].SupportedEndpointTypes, constant.EndpointTypeOpenAIVideo)
	require.Equal(t, "video", byID["kling-v3-omni"].Capability)
	require.NotContains(t, byID, "grok-other-group")
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

func TestMiaModelCapabilityExcludesResponseOnlyModels(t *testing.T) {
	capability, ok := miaModelCapability([]constant.EndpointType{constant.EndpointTypeOpenAIResponse})
	require.False(t, ok)
	require.Empty(t, capability)
}
