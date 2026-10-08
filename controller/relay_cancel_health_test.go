package controller

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCanceledRelayHealthUsesOnlyFinalDispatch(t *testing.T) {
	old := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = old; service.ClearChannelHealth(270) })
	for _, tc := range []struct {
		name     string
		alter    func(*gin.Context, *relaycommon.RelayInfo)
		observed bool
	}{
		{"long zero response", func(c *gin.Context, i *relaycommon.RelayInfo) {}, true},
		{"short final retry despite long total", func(c *gin.Context, i *relaycommon.RelayInfo) {
			c.Set("channel_health_attempt_started_at", time.Now().Add(-time.Second))
		}, false},
		{"partial output", func(c *gin.Context, i *relaycommon.RelayInfo) { i.ReceivedResponseCount = 1 }, false},
		{"first response arrived", func(c *gin.Context, i *relaycommon.RelayInfo) { i.FirstResponseTime = time.Now().Add(-time.Minute) }, false},
		{"upstream data arrived", func(c *gin.Context, i *relaycommon.RelayInfo) { i.LastDataTime = time.Now() }, false},
		{"hedge request", func(c *gin.Context, i *relaycommon.RelayInfo) { c.Set("channel_health_hedged_request", true) }, false},
		{"wrong channel attribution", func(c *gin.Context, i *relaycommon.RelayInfo) { c.Set("channel_health_attempt_channel_id", 81) }, false},
		{"missing dispatched attempt", func(c *gin.Context, i *relaycommon.RelayInfo) {
			c.Set("channel_health_attempt_started_at", time.Time{})
		}, false},
		{"image request", func(c *gin.Context, i *relaycommon.RelayInfo) { i.RelayFormat = types.RelayFormatOpenAIImage }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service.ClearChannelHealth(270)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			ctx, cancel := context.WithCancel(c.Request.Context())
			cancel()
			c.Request = c.Request.WithContext(ctx)
			common.SetContextKey(c, constant.ContextKeyChannelAutoBan, true)
			c.Set("channel_health_attempt_channel_id", 270)
			c.Set("channel_health_attempt_started_at", time.Now().Add(-180*time.Second))
			i := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 270, ChannelType: constant.ChannelTypeOpenAI}, StartTime: time.Now().Add(-5 * time.Minute), OriginModelName: "gpt-6.1-sol", IsStream: true, RelayFormat: types.RelayFormatOpenAI}
			tc.alter(c, i)
			other := map[string]interface{}{}
			observeCanceledRelayHealth(c, i, other)
			if tc.observed {
				require.Equal(t, "suspected_upstream_no_response", other["channel_health_observation"])
				require.Equal(t, false, other["channel_health_probe_requested"])
				for n := 0; n < 10; n++ {
					observeCanceledRelayHealth(c, i, other)
				}
				ch := types.ChannelError{ChannelId: 270, AutoBan: true}
				for n := 0; n < 3; n++ {
					action, _ := service.EvaluateChannelNoResponseCancellation(ch, relayProbeTarget(i), 180*time.Second, 0)
					require.Equal(t, service.HealthSkip, action, "one terminal request counts only once")
				}
				action, _ := service.EvaluateChannelNoResponseCancellation(ch, relayProbeTarget(i), 180*time.Second, 0)
				require.Equal(t, service.HealthProbeBeforeDisable, action)
			} else {
				require.Empty(t, other)
			}
		})
	}
}

func TestSlowCanceledRelayHealthKeepsAuditAndQuota(t *testing.T) {
	db := setupModelDataToggleTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Log{}, &model.CancellationObservation{}))
	oldLogDB, oldRedis, oldEnabled := model.LOG_DB, common.RedisEnabled, common.AutomaticDisableChannelEnabled
	model.LOG_DB, common.RedisEnabled, common.AutomaticDisableChannelEnabled = db, false, true
	t.Cleanup(func() {
		model.LOG_DB, common.RedisEnabled, common.AutomaticDisableChannelEnabled = oldLogDB, oldRedis, oldEnabled
		service.ClearChannelHealth(270)
	})
	service.ClearChannelHealth(270)
	require.NoError(t, db.Create(&model.User{Id: 123, Username: "slow-cancel-test", Quota: 100000}).Error)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	ctx, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(ctx)
	common.SetContextKey(c, constant.ContextKeyChannelAutoBan, true)
	c.Set(common.RequestIdKey, "slow-cancel-audit-test")
	c.Set("channel_id", 270)
	c.Set("channel_health_attempt_channel_id", 270)
	c.Set("channel_health_attempt_started_at", time.Now().Add(-180*time.Second))
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 270, ChannelType: constant.ChannelTypeOpenAI}, UserId: 123, TokenId: 456, OriginModelName: "gpt-6.1-sol", StartTime: time.Now().Add(-180 * time.Second), IsStream: true, RelayFormat: types.RelayFormatOpenAI}
	recordCanceledRelayLog(c, info)
	recordCanceledRelayLog(c, info)
	var rows []model.Log
	require.NoError(t, db.Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Zero(t, rows[0].Quota)
	var other map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(rows[0].Other, &other))
	require.Equal(t, float64(499), other["status_code"])
	require.Equal(t, "client_canceled", other["error_code"])
	require.Equal(t, "suspected_upstream_no_response", other["channel_health_observation"])
	require.Equal(t, false, other["channel_health_probe_requested"])
	require.Equal(t, "pending_reconciliation", other["accounting_status"])
	var user model.User
	require.NoError(t, db.First(&user, 123).Error)
	require.Equal(t, 100000, user.Quota)
}
