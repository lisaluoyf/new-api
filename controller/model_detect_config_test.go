package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestModelDetectSkipChannelsSaveReadAndCompatibility(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.ChannelDetectLog{}))
	previousDB := model.DB
	model.DB = db
	common.OptionMapRWMutex.Lock()
	previousOptions := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		model.DB = previousDB
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
	})
	post := func(body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/api/admin/model-detect-config", strings.NewReader(body))
		SaveModelDetectConfig(ctx)
		return recorder
	}
	recorder := post(`{"model":"model-a","fingerprint_enabled":true,"fingerprint_interval_minutes":7,"fingerprint_skip_channel_ids":[224,38,38],"uptime_enabled":true,"uptime_interval_minutes":3}`)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	cfg := service.LoadDetectConfig("model-a")
	require.Equal(t, []int{38, 224}, cfg.FingerprintSkipChannelIDs)
	require.True(t, cfg.FingerprintEnabled)
	require.True(t, cfg.UptimeEnabled)
	require.Equal(t, 7, cfg.FingerprintIntervalMinutes)
	require.Equal(t, 3, cfg.UptimeIntervalMinutes)
	require.Equal(t, []int{38, 224}, buildModelDetectConfigResponse("model-a")["fingerprint_skip_channel_ids"])
	require.Equal(t, []int{}, buildModelDetectConfigResponse("new-model")["fingerprint_skip_channel_ids"])

	// An older client changing an interval must not erase the skip list.
	recorder = post(`{"model":"model-a","fingerprint_enabled":true,"fingerprint_interval_minutes":9,"uptime_enabled":false}`)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, []int{38, 224}, service.LoadDetectConfig("model-a").FingerprintSkipChannelIDs)

	for _, invalid := range []string{"[0]", "[-1]", "[2147483648]", "[38.5]", `"38,224"`} {
		recorder = post(`{"model":"model-a","fingerprint_skip_channel_ids":` + invalid + `}`)
		require.Equal(t, http.StatusBadRequest, recorder.Code, invalid)
		require.Equal(t, []int{38, 224}, service.LoadDetectConfig("model-a").FingerprintSkipChannelIDs)
	}
	// [] explicitly clears the list; older persisted configs also default to [].
	recorder = post(`{"model":"model-a","fingerprint_enabled":true,"fingerprint_skip_channel_ids":[]}`)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, []int{}, service.LoadDetectConfig("model-a").FingerprintSkipChannelIDs)
	require.NoError(t, db.Save(&model.Option{Key: service.DetectConfigKey("legacy"), Value: `{"fingerprint_enabled":true}`}).Error)
	require.Equal(t, []int{}, service.LoadDetectConfig("legacy").FingerprintSkipChannelIDs)
}
