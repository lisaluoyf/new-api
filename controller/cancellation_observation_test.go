package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCancellationObservationRootOnlyReadAPI(t *testing.T) {
	db := setupModelDataToggleTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.CancellationObservation{}))
	require.NoError(t, model.CreateCancellationObservation(&model.CancellationObservation{
		RequestId: "private-cancel", UserId: 123, TokenId: 456, ChannelId: 97, ModelName: "glm-5.3-flash",
	}))
	for _, role := range []int{common.RoleCommonUser, common.RoleAdminUser, common.RoleRootUser} {
		r := gin.New()
		r.Use(sessions.Sessions("test", cookie.NewStore([]byte("test-cancellation-observation-key"))))
		r.Use(func(c *gin.Context) {
			s := sessions.Default(c)
			s.Set("username", "test")
			s.Set("id", 1)
			s.Set("role", role)
			s.Set("status", common.UserStatusEnabled)
			c.Next()
		})
		r.GET("/cases", middleware.AdminAuth(), middleware.RootAuth(), ListCancellationObservations)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/cases?model=glm-5.3-flash", nil))
		var response map[string]interface{}
		require.NoError(t, common.Unmarshal(w.Body.Bytes(), &response))
		if role != common.RoleRootUser {
			require.Equal(t, false, response["success"])
			require.NotContains(t, w.Body.String(), "private-cancel")
		} else {
			require.Equal(t, true, response["success"])
			require.Contains(t, w.Body.String(), "private-cancel")
			require.Contains(t, w.Body.String(), `"automatic_charge_allowed":false`)
		}
	}
}
