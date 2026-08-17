package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGPTSubscriptionRollingLimitErrorUsesUserTimezone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	availableAt := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC).Unix()
	relayInfo := &relaycommon.RelayInfo{UserSetting: dto.UserSetting{Language: "zh-CN", Timezone: "Europe/Moscow"}}
	limitErr := &model.GPTSubscriptionRollingLimitError{Info: model.GPTSubscriptionRollingLimitInfo{
		LimitedWindows: []string{"5h", "7d"}, AvailableAt: availableAt, RetryAfterSeconds: 600,
		FiveHourUsed: 100, FiveHourLimit: 100, SevenDayUsed: 200, SevenDayLimit: 200, RequestedQuota: 10,
	}}

	apiErr := newGPTSubscriptionRollingLimitAPIError(c, relayInfo, limitErr, 0, true)
	require.Equal(t, types.ErrorCodeGPTSubscriptionRollingLimit, apiErr.GetErrorCode())
	require.Contains(t, apiErr.Error(), "5 小时和7 天滚动额度")
	require.Contains(t, apiErr.Error(), "2026年8月17日 15:00（Europe/Moscow）")
	require.Contains(t, apiErr.Error(), "钱包余额不足")

	openAIError := apiErr.ToOpenAIError()
	require.NotEmpty(t, openAIError.Metadata)
	var metadata map[string]any
	require.NoError(t, common.Unmarshal(openAIError.Metadata, &metadata))
	require.Equal(t, "Europe/Moscow", metadata["timezone"])
	require.Equal(t, "gpt_subscription_rolling_limit", metadata["reason"])
}

func TestGPTSubscriptionRollingLimitErrorFallsBackToUTC(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Request.Header.Set("X-Timezone", "not/a-timezone")
	availableAt := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC).Unix()
	relayInfo := &relaycommon.RelayInfo{UserSetting: dto.UserSetting{Language: "en", Timezone: "also-invalid"}}
	limitErr := &model.GPTSubscriptionRollingLimitError{Info: model.GPTSubscriptionRollingLimitInfo{
		LimitedWindows: []string{"5h"}, AvailableAt: availableAt,
	}}

	apiErr := newGPTSubscriptionRollingLimitAPIError(c, relayInfo, limitErr, 0, true)
	require.Contains(t, apiErr.Error(), "Aug 17, 2026 12:00 (UTC)")
	var metadata map[string]any
	require.NoError(t, common.Unmarshal(apiErr.Metadata, &metadata))
	require.Equal(t, "UTC", metadata["timezone"])
}
