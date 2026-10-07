package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

func TestTextSettlementPendingDoesNotRefundOrRecordFalseConsumption(t *testing.T) {
	truncate(t)
	initial := common.GetTrustQuota() + 10000
	seedUser(t, 1, initial)
	seedToken(t, 2, 1, "retry-billing-key", initial)
	c := retryBillingContext()
	c.Set("token_quota", initial)
	c.Set(common.RequestIdKey, "durable-service-request")
	info := retryBillingInfo(initial)
	info.RequestId = "durable-service-request"
	info.StartTime = time.Now()
	info.ForcePreConsume = true
	info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: 94}
	require.NoError(t, model.DB.Create(&model.Channel{Id: 94}).Error)
	info.SetWalletPriceData(types.PriceData{ModelRatio: 1, CompletionRatio: 2, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}})
	info.ActivateWalletPriceData()
	require.Nil(t, PreConsumeBilling(c, 1000, info))
	// Another concurrent request exhausted the remaining wallet after precharge.
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 1).Update("quota", 0).Error)
	usage := &dto.Usage{PromptTokens: 2000, TotalTokens: 2000, UsageSource: "upstream_reported"}
	require.NoError(t, PostTextConsumeQuota(c, info, usage, nil))
	require.NoError(t, PostTextConsumeQuota(c, info, usage, nil))
	require.Error(t, SettleBilling(c, info, 2000))
	require.NoError(t, info.Billing.RefundSync(c))
	var user model.User
	require.NoError(t, model.DB.First(&user, 1).Error)
	require.Zero(t, user.Quota)
	require.Zero(t, user.RequestCount)
	var log model.Log
	require.NoError(t, model.LOG_DB.Where("request_id = ?", info.RequestId).First(&log).Error)
	require.Equal(t, model.LogTypeError, log.Type)
	require.Zero(t, log.Quota)
	require.Equal(t, 2000, log.PromptTokens)
	originalID := log.Id
	_, apiErr := NewBillingSession(c, retryBillingInfo(initial), 1000)
	require.NotNil(t, apiErr, "new paid calls must be blocked while usage is unpaid")
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 1).Update("quota", 5000).Error)
	require.NoError(t, model.RetryTextSettlements(common.GetTimestamp()+120))
	require.NoError(t, model.DB.First(&user, 1).Error)
	require.Equal(t, 4000, user.Quota)
	require.Equal(t, 2000, user.UsedQuota)
	require.Equal(t, 1, user.RequestCount)
	require.NoError(t, model.LOG_DB.Where("request_id = ?", info.RequestId).First(&log).Error)
	require.Equal(t, originalID, log.Id)
	require.Equal(t, model.LogTypeConsume, log.Type)
	require.Equal(t, 2000, log.Quota)
}
