package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestChannelRejectionLogsPreserveHealthAttribution(t *testing.T) {
	db := setupModelDataToggleTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Log{}))
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "health-log-test", Setting: `{}`}).Error)
	base := "https://upstream.example"
	require.NoError(t, db.Create(&model.Channel{Id: 82, Name: "health-log-test", BaseURL: &base, Key: "test-key", Models: "gpt-6-astra"}).Error)
	oldLog, oldRedis, oldEnabled, oldAuto := model.LOG_DB, common.RedisEnabled, constant.ErrorLogEnabled, common.AutomaticDisableChannelEnabled
	model.LOG_DB, common.RedisEnabled, constant.ErrorLogEnabled, common.AutomaticDisableChannelEnabled = db, false, true, false
	t.Cleanup(func() {
		model.LOG_DB, common.RedisEnabled, constant.ErrorLogEnabled, common.AutomaticDisableChannelEnabled = oldLog, oldRedis, oldEnabled, oldAuto
	})
	for _, tc := range []struct {
		name, message, code, param, want string
		status                           int
	}{
		{"opaque refusal", "请求被拒绝", "", "", "upstream_failure", 400},
		{"invalid parameter", "请求被拒绝", "", "temperature", "request_parameter", 400},
		{"known parameter code", "请求被拒绝", "context_length_exceeded", "", "request_parameter", 400},
		{"explicit policy", "The request was rejected by a content policy.", "cyber_policy", "", "content_policy", 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			c.Set("id", 1)
			c.Set("username", "health-log-test")
			c.Set("channel_id", 82)
			c.Set("original_model", "gpt-6-astra")
			c.Set(common.RequestIdKey, tc.name)
			err := types.WithOpenAIError(types.OpenAIError{Message: tc.message, Type: "invalid_request_error", Code: tc.code, Param: tc.param}, tc.status)
			processChannelError(c, &relaycommon.RelayInfo{OriginModelName: "gpt-6-astra"}, types.ChannelError{ChannelId: 82, AutoBan: true}, err)
			var l model.Log
			require.NoError(t, db.Where("request_id = ?", tc.name).First(&l).Error)
			var other map[string]any
			require.NoError(t, common.UnmarshalJsonStr(l.Other, &other))
			require.Equal(t, tc.want, other["error_category"])
			require.EqualValues(t, tc.status, other["status_code"])
			require.Contains(t, l.Content, tc.message)
		})
	}
}
