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
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCanceledRelayPersistsAuditWithoutChargingOrHealthPenalty(t *testing.T) {
	db := setupModelDataToggleTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Log{}, &model.CancellationObservation{}))
	oldLogDB, oldRedis, oldErrorLog := model.LOG_DB, common.RedisEnabled, constant.ErrorLogEnabled
	model.LOG_DB, common.RedisEnabled, constant.ErrorLogEnabled = db, false, false
	t.Cleanup(func() { model.LOG_DB, common.RedisEnabled, constant.ErrorLogEnabled = oldLogDB, oldRedis, oldErrorLog })
	require.NoError(t, db.Create(&model.User{Id: 123, Username: "cancel-test", Quota: 100000}).Error)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	ctx, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(ctx)
	c.Set("channel_id", 94)
	c.Set(common.RequestIdKey, "cancel-audit-test")
	info := &relaycommon.RelayInfo{UserId: 123, TokenId: 456, OriginModelName: "gpt-test", StartTime: time.Now(), IsStream: true, ReceivedResponseCount: 17}
	recordCanceledRelayLog(c, info)
	var count int64
	require.NoError(t, db.Model(&model.Log{}).Count(&count).Error)
	require.Zero(t, count)
	cancel()
	// Per-attempt cancellation still skips channel health and produces no row.
	processChannelError(c, info, *types.NewChannelError(94, 1, "test", false, "", false), types.NewError(context.Canceled, types.ErrorCodeBadResponse))
	require.NoError(t, db.Model(&model.Log{}).Count(&count).Error)
	require.Zero(t, count)
	recordCanceledRelayLog(c, info)
	recordCanceledRelayLog(c, info)
	var rows []model.Log
	require.NoError(t, db.Find(&rows).Error)
	require.Len(t, rows, 1)
	row := rows[0]
	require.Equal(t, model.LogTypeError, row.Type)
	require.Equal(t, "cancel-audit-test", row.RequestId)
	require.Equal(t, 123, row.UserId)
	require.Equal(t, 94, row.ChannelId)
	require.Zero(t, row.Quota)
	require.Zero(t, row.PromptTokens)
	var other map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(row.Other, &other))
	require.Equal(t, float64(499), other["status_code"])
	require.Equal(t, "pending_reconciliation", other["accounting_status"])
	require.Contains(t, other, "upstream_cost")
	require.Nil(t, other["upstream_cost"])
	var user model.User
	require.NoError(t, db.First(&user, 123).Error)
	require.Equal(t, 100000, user.Quota)
	item, err := model.GetCancellationObservation("cancel-audit-test")
	require.NoError(t, err)
	require.Equal(t, "live_cancel", item.Source)
	require.Equal(t, 123, item.UserId)
	require.Equal(t, 456, item.TokenId)
	require.Equal(t, 17, item.ReceivedResponses)
	require.Contains(t, item.RequestSnapshot, `"automatic_charge_allowed":false`)
}
