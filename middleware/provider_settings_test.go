package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPlaygroundRoutingUsesAuthenticatedProviderSettings(t *testing.T) {
	i18n.Init()
	oldDB, oldRedis := model.DB, common.RedisEnabled
	t.Cleanup(func() {
		model.DB, common.RedisEnabled = oldDB, oldRedis
		service.InvalidateChannelRoutingCache()
	})
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB, common.RedisEnabled = db, false
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Channel{}, &model.Ability{}, &model.ChannelModelPricing{}))
	user := model.User{Id: 42, Username: "provider-playground-owner", Group: "default", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	const modelName = "provider-playground-test"
	priceRatio := 1.0
	for _, channelID := range []int{1, 2} {
		channel := model.Channel{Id: channelID, Name: "provider", Key: "test-key", Models: modelName, Group: "default", Status: common.ChannelStatusEnabled, ApimasterPriceRatio: &priceRatio}
		require.NoError(t, db.Create(&channel).Error)
		require.NoError(t, db.Create(&model.ChannelModelPricing{ChannelId: channelID, ModelName: modelName, InputPrice: float64(channelID), GroupRatio: 1, PricingSource: "api"}).Error)
	}
	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("provider-playground-session-test-key"))))
	router.POST("/login", func(ctx *gin.Context) {
		session := sessions.Default(ctx)
		session.Set("id", user.Id)
		session.Set("username", user.Username)
		session.Set("role", user.Role)
		session.Set("status", user.Status)
		session.Set("group", user.Group)
		require.NoError(t, session.Save())
		ctx.Status(http.StatusOK)
	})
	selectedChannel := 0
	router.POST("/pg/chat/completions", UserAuth(), Distribute(), func(ctx *gin.Context) {
		selectedChannel = common.GetContextKeyInt(ctx, constant.ContextKeyChannelId)
		ctx.Status(http.StatusOK)
	})
	login := httptest.NewRecorder()
	router.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/login", nil))
	require.Equal(t, http.StatusOK, login.Code)
	sessionCookie := login.Result().Cookies()[0]
	for _, testCase := range []struct {
		name     string
		mode     string
		ids      []int
		enabled  bool
		selected int
		status   int
	}{
		{"include", "include", []int{2}, true, 2, http.StatusOK},
		{"exclude", "exclude", []int{1}, true, 2, http.StatusOK},
		{"include keeps price ordering", "include", []int{1, 2}, true, 1, http.StatusOK},
		{"disabled", "include", []int{2}, false, 1, http.StatusOK},
		{"no rule", "", nil, false, 1, http.StatusOK},
		{"exhausted include", "include", []int{99}, true, 0, http.StatusServiceUnavailable},
		{"exhausted exclude", "exclude", []int{1, 2}, true, 0, http.StatusServiceUnavailable},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			settings := dto.UserSetting{}
			if testCase.mode != "" {
				settings.ProviderSettings = []dto.ProviderSetting{{Model: modelName, Enabled: testCase.enabled, Mode: testCase.mode, ChannelIDs: testCase.ids}}
			}
			user.SetSetting(settings)
			require.NoError(t, db.Model(&user).Update("setting", user.Setting).Error)
			service.InvalidateChannelRoutingCache()
			selectedChannel = 0
			request := httptest.NewRequest(http.MethodPost, "/pg/chat/completions", strings.NewReader(`{"model":"`+modelName+`","messages":[]}`))
			request.Header.Set("Content-Type", "application/json")
			request.AddCookie(sessionCookie)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, testCase.status, response.Code, response.Body.String())
			require.Equal(t, testCase.selected, selectedChannel)
		})
	}
}
