package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRevokeInternalSessionsInvalidatesBrowserCookies(t *testing.T) {
	previousDB, previousRedis, previousKey := model.DB, common.RedisEnabled, common.ApimasterInternalSyncKey
	t.Cleanup(func() {
		model.DB, common.RedisEnabled, common.ApimasterInternalSyncKey = previousDB, previousRedis, previousKey
	})
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}))
	model.DB, common.RedisEnabled, common.ApimasterInternalSyncKey = db, false, "reset-test-key"
	passwordHash, err := common.Password2Hash("stable-derived-password")
	require.NoError(t, err)
	user := model.User{Username: "resetmirroraccount001", Password: passwordHash, Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)

	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("test-password-reset-session-key"))))
	router.POST("/login", InternalLogin)
	router.POST("/revoke", middleware.RequireApimasterInternalSync(), RevokeInternalSessions)
	router.GET("/protected", middleware.UserAuth(), func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })
	router.GET("/optional", middleware.TryUserAuth(), func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{"authenticated": ctx.GetInt("id") == user.Id})
	})

	login := func() *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(`{"username":"resetmirroraccount001","password":"stable-derived-password"}`))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(response, request)
		return response
	}
	visit := func(path string, sessionCookie *http.Cookie) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(sessionCookie)
		router.ServeHTTP(response, request)
		return response
	}

	first := login()
	require.Contains(t, first.Body.String(), `"success":true`)
	oldCookie := first.Result().Cookies()[0]
	require.Equal(t, http.StatusNoContent, visit("/protected", oldCookie).Code)
	require.Contains(t, visit("/optional", oldCookie).Body.String(), `"authenticated":true`)

	withoutKey := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/revoke", strings.NewReader(`{"username":"resetmirroraccount001"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(withoutKey, request)
	require.Equal(t, http.StatusUnauthorized, withoutKey.Code)
	require.Equal(t, http.StatusNoContent, visit("/protected", oldCookie).Code)

	revoked := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/revoke", strings.NewReader(`{"username":"resetmirroraccount001"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Apimaster-Internal-Key", common.ApimasterInternalSyncKey)
	router.ServeHTTP(revoked, request)
	require.Contains(t, revoked.Body.String(), `"revoked":true`)
	require.Equal(t, http.StatusUnauthorized, visit("/protected", oldCookie).Code)
	require.Contains(t, visit("/optional", oldCookie).Body.String(), `"authenticated":false`)

	second := login()
	require.Contains(t, second.Body.String(), `"success":true`)
	require.Equal(t, http.StatusNoContent, visit("/protected", second.Result().Cookies()[0]).Code)
	var persisted model.User
	require.NoError(t, db.First(&persisted, user.Id).Error)
	require.Equal(t, int64(1), persisted.SessionVersion)
	require.Equal(t, passwordHash, persisted.Password)
}
