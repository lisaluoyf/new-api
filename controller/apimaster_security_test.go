package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestInternalAccountSecurityRequiresTrustedCallerAndRealFactor(t *testing.T) {
	oldDB, oldKey := model.DB, common.ApimasterInternalSyncKey
	t.Cleanup(func() { model.DB, common.ApimasterInternalSyncKey = oldDB, oldKey })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TwoFA{}, &model.TwoFABackupCode{}))
	model.DB, common.ApimasterInternalSyncKey = db, "trusted-server-test-key"
	u := model.User{Username: "website-mirror", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&u).Error)
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "Test", AccountName: u.Username})
	require.NoError(t, err)
	f := model.TwoFA{UserId: u.Id, Secret: key.Secret(), IsEnabled: true}
	require.NoError(t, db.Create(&f).Error)
	router := gin.New()
	router.POST("/security", middleware.RequireApimasterInternalSync(), InternalAccountSecurity)
	invoke := func(action, code, auth string) *httptest.ResponseRecorder {
		raw, err := common.Marshal(map[string]string{"username": u.Username, "action": action, "code": code})
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, "/security", strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Apimaster-Internal-Key", auth)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	require.Equal(t, 401, invoke("status", "", "").Code)
	status := invoke("status", "", common.ApimasterInternalSyncKey)
	require.Contains(t, status.Body.String(), `"enabled":true`)
	require.Empty(t, status.Result().Cookies())
	require.NotContains(t, status.Body.String(), key.Secret())
	require.Equal(t, 401, invoke("verify", "not-a-code", common.ApimasterInternalSyncKey).Code)
	code, err := totp.GenerateCode(key.Secret(), time.Now())
	require.NoError(t, err)
	require.Contains(t, invoke("verify", code, common.ApimasterInternalSyncKey).Body.String(), `"verified":true`)
	require.Equal(t, 401, invoke("verify", code, common.ApimasterInternalSyncKey).Code)
	backup, err := common.GenerateBackupCodes()
	require.NoError(t, err)
	require.NoError(t, model.CreateBackupCodes(u.Id, backup))
	require.Contains(t, invoke("verify", backup[0], common.ApimasterInternalSyncKey).Body.String(), `"verified":true`)
	require.Equal(t, 401, invoke("verify", backup[0], common.ApimasterInternalSyncKey).Code)
	require.NoError(t, db.Model(&f).Update("locked_until", time.Now().Add(time.Minute)).Error)
	require.Equal(t, 429, invoke("verify", backup[1], common.ApimasterInternalSyncKey).Code)
	require.NoError(t, db.Model(&u).Update("status", common.UserStatusDisabled).Error)
	require.Equal(t, 403, invoke("status", "", common.ApimasterInternalSyncKey).Code)
}
