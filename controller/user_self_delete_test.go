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

func TestDeleteSelfRevokesOldSessionsAndInternalLogin(t *testing.T) {
	oldDB, oldIdentity, oldRedis, oldLogin := model.DB, model.APIMASTER_PG_DB, common.RedisEnabled, common.PasswordLoginEnabled
	t.Cleanup(func() {
		model.DB, model.APIMASTER_PG_DB, common.RedisEnabled, common.PasswordLoginEnabled = oldDB, oldIdentity, oldRedis, oldLogin
	})
	t.Setenv("APIMASTER_PG_DSN", "")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}))
	model.DB, model.APIMASTER_PG_DB, common.RedisEnabled, common.PasswordLoginEnabled = db, nil, false, true
	hash, err := common.Password2Hash("derived-password")
	require.NoError(t, err)
	user := model.User{Username: "dcd605b16990480ca3f1", Password: hash, Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AffCode: "delete-test"}
	require.NoError(t, db.Create(&user).Error)
	r := gin.New()
	r.Use(sessions.Sessions("session", cookie.NewStore([]byte("test-account-deletion-session-key"))))
	r.POST("/internal-login", InternalLogin)
	r.DELETE("/self", middleware.UserAuth(), DeleteSelf)
	r.GET("/protected", middleware.UserAuth(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	login := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/internal-login", strings.NewReader(`{"username":"dcd605b16990480ca3f1","password":"derived-password"}`))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		return w
	}
	before := login()
	require.Contains(t, before.Body.String(), `"success":true`)
	oldCookie := before.Result().Cookies()[0]
	w := httptest.NewRecorder()
	deletion := httptest.NewRequest(http.MethodDelete, "/self", nil)
	deletion.AddCookie(oldCookie)
	r.ServeHTTP(w, deletion)
	require.Contains(t, w.Body.String(), `"success":true`)
	cleared := map[string]bool{}
	for _, c := range w.Result().Cookies() {
		if c.MaxAge < 0 {
			cleared[c.Name] = true
		}
	}
	for _, name := range []string{"session", "apimaster_session", "apimaster_newapi_user"} {
		require.True(t, cleared[name], name)
	}
	// A copied cookie from a second browser must be rejected server-side.
	w = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.AddCookie(oldCookie)
	r.ServeHTTP(w, request)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Contains(t, login().Body.String(), `"success":false`)
	var persisted model.User
	require.NoError(t, db.Unscoped().First(&persisted, user.Id).Error)
	require.True(t, persisted.DeletedAt.Valid)
}
