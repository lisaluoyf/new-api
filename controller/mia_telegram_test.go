package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupMiaTelegramTestRouter(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	common.RedisEnabled = false
	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false

	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}))
	model.DB = db
	model.LOG_DB = db

	previousSecret := common.MiaInternalServiceKey
	common.MiaInternalServiceKey = "test-mia-internal-secret"
	t.Cleanup(func() {
		common.MiaInternalServiceKey = previousSecret
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
	return router, db
}

func performMiaTelegramRequest(router *gin.Engine, secret string, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/user/internal/telegram-api-key", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	if secret != "" {
		request.Header.Set("X-Mia-Internal-Key", secret)
	}
	router.ServeHTTP(recorder, request)
	return recorder
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
		TelegramId: "123456",
	}
	require.NoError(t, db.Create(&user).Error)

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
