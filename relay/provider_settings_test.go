package relay

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupProviderTaskTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	oldDB, oldRedis := model.DB, common.RedisEnabled
	t.Cleanup(func() { model.DB, common.RedisEnabled = oldDB, oldRedis })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB, common.RedisEnabled = db, false
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Channel{}, &model.ChannelModelPricing{}, &model.Task{}, &model.Midjourney{}))
	return db
}

func TestVideoRemixRespectsOriginChannelProviderSettings(t *testing.T) {
	db := setupProviderTaskTestDB(t)
	baseURL := "https://provider.invalid"
	channel := model.Channel{Id: 38, Type: constant.ChannelTypeOpenAI, Name: "origin-provider", Key: "origin-key", Status: common.ChannelStatusEnabled, Models: "sora-2", BaseURL: &baseURL}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, db.Create(&model.Task{TaskID: "origin-video", UserId: 42, ChannelId: 38, Properties: model.Properties{OriginModelName: "sora-2"}}).Error)
	for _, testCase := range []struct {
		name    string
		mode    string
		ids     []int
		enabled bool
		allowed bool
	}{
		{"include rejects origin", "include", []int{39}, true, false},
		{"exclude rejects origin", "exclude", []int{38}, true, false},
		{"include allows origin", "include", []int{38}, true, true},
		{"exclude allows origin", "exclude", []int{39}, true, true},
		{"disabled rule", "exclude", []int{38}, false, true},
		{"no rule", "", nil, false, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			settings := dto.UserSetting{}
			if testCase.mode != "" {
				settings.ProviderSettings = []dto.ProviderSetting{{Model: "sora-2", Mode: testCase.mode, ChannelIDs: testCase.ids, Enabled: testCase.enabled}}
			}
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/videos/origin-video/remix", nil)
			ctx.Params = gin.Params{{Key: "video_id", Value: "origin-video"}}
			ctx.Set("provider_settings_request_model", "")
			common.SetContextKey(ctx, constant.ContextKeyChannelId, 39)
			common.SetContextKey(ctx, constant.ContextKeyChannelKey, "selected-key")
			common.SetContextKey(ctx, constant.ContextKeyUserSetting, settings)
			info := &relaycommon.RelayInfo{UserId: 42, TaskRelayInfo: &relaycommon.TaskRelayInfo{}, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 39}}
			taskErr := ResolveOriginTask(ctx, info)
			if !testCase.allowed {
				require.NotNil(t, taskErr)
				require.Equal(t, "provider_channel_not_allowed", taskErr.Code)
				require.Equal(t, http.StatusForbidden, taskErr.StatusCode)
				require.Nil(t, info.LockedChannel)
				require.Equal(t, 39, info.ChannelId)
				require.Equal(t, 39, common.GetContextKeyInt(ctx, constant.ContextKeyChannelId))
				require.Equal(t, "selected-key", common.GetContextKeyString(ctx, constant.ContextKeyChannelKey))
				return
			}
			require.Nil(t, taskErr)
			require.Equal(t, "sora-2", info.OriginModelName)
			require.Equal(t, 38, info.LockedChannel.(*model.Channel).Id)
			require.Equal(t, 38, info.ChannelId)
		})
	}
}

func TestMidjourneyContinuationRejectsDisallowedOriginChannel(t *testing.T) {
	db := setupProviderTaskTestDB(t)
	baseURL := "https://origin-provider.invalid"
	require.NoError(t, db.Create(&model.Channel{Id: 38, Type: constant.ChannelTypeMidjourney, Key: "origin-key", BaseURL: &baseURL, Status: common.ChannelStatusEnabled}).Error)
	require.NoError(t, db.Create(&model.Midjourney{MjId: "origin-image", UserId: 42, ChannelId: 38, Status: "SUCCESS"}).Error)
	for _, action := range []string{constant.MjActionUpscale, constant.MjActionVariation} {
		for _, mode := range []string{"include", "exclude"} {
			t.Run(action+"/"+mode, func(t *testing.T) {
				ids := []int{38}
				if mode == "include" {
					ids = []int{39}
				}
				ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
				body, err := common.Marshal(dto.MidjourneyRequest{TaskId: "origin-image", Action: action, Index: 1})
				require.NoError(t, err)
				ctx.Request = httptest.NewRequest(http.MethodPost, "/mj/submit/change", strings.NewReader(string(body)))
				ctx.Request.Header.Set("Content-Type", "application/json")
				ctx.Request.Header.Set("Authorization", "Bearer selected-key")
				common.SetContextKey(ctx, constant.ContextKeyChannelId, 39)
				common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeMidjourney)
				common.SetContextKey(ctx, constant.ContextKeyChannelBaseUrl, "https://selected-provider.invalid")
				name := "mj_" + strings.ToLower(action)
				common.SetContextKey(ctx, constant.ContextKeyUserSetting, dto.UserSetting{ProviderSettings: []dto.ProviderSetting{{Model: name, Mode: mode, ChannelIDs: ids, Enabled: true}}})
				info := &relaycommon.RelayInfo{UserId: 42, OriginModelName: name, RelayMode: relayconstant.RelayModeMidjourneyChange}
				response := RelayMidjourneySubmit(ctx, info)
				require.NotNil(t, response)
				require.Contains(t, response.Description, "provider settings")
				require.Equal(t, 39, common.GetContextKeyInt(ctx, constant.ContextKeyChannelId))
				require.Equal(t, "https://selected-provider.invalid", common.GetContextKeyString(ctx, constant.ContextKeyChannelBaseUrl))
				require.Equal(t, "Bearer selected-key", ctx.Request.Header.Get("Authorization"))
			})
		}
	}
}

func TestProviderSettingsDoNotBlockExistingTaskQueries(t *testing.T) {
	db := setupProviderTaskTestDB(t)
	service.InitHttpClient()
	var upstreamRequests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upstreamRequests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"code":1,"description":"success","result":"saved-seed"}`))
	}))
	t.Cleanup(upstream.Close)
	baseURL := upstream.URL
	require.NoError(t, db.Create(&model.Channel{Id: 38, Type: constant.ChannelTypeMidjourney, Key: "origin-key", BaseURL: &baseURL, Status: common.ChannelStatusEnabled}).Error)
	require.NoError(t, db.Create(&model.Midjourney{MjId: "existing-image", UserId: 42, ChannelId: 38, Status: "SUCCESS"}).Error)
	require.NoError(t, db.Create(&model.Task{TaskID: "existing-video", UserId: 42, ChannelId: 38, Status: model.TaskStatusSuccess, Properties: model.Properties{OriginModelName: "sora-2"}}).Error)
	settings := dto.UserSetting{ProviderSettings: []dto.ProviderSetting{
		{Model: "mj_imagine", Mode: "exclude", ChannelIDs: []int{38}, Enabled: true},
		{Model: "sora-2", Mode: "exclude", ChannelIDs: []int{38}, Enabled: true},
	}}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/mj/task/existing-image/image-seed", nil)
	ctx.Params = gin.Params{{Key: "id", Value: "existing-image"}}
	ctx.Set("id", 42)
	common.SetContextKey(ctx, constant.ContextKeyUserSetting, settings)
	require.Nil(t, RelayMidjourneyTaskImageSeed(ctx))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "saved-seed")
	require.Equal(t, int32(1), upstreamRequests.Load())
	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/video/generations/existing-video", nil)
	ctx.Params = gin.Params{{Key: "task_id", Value: "existing-video"}}
	ctx.Set("id", 42)
	common.SetContextKey(ctx, constant.ContextKeyUserSetting, settings)
	require.Nil(t, RelayTaskFetch(ctx, relayconstant.RelayModeVideoFetchByID))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "existing-video")
}
