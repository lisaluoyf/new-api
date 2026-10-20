package controller

import (
	"context"
	"errors"
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

func TestBackupFailuresEnterHealthWindowBeforePrimaryFinishes(t *testing.T) {
	oldEnabled, oldLogs := common.AutomaticDisableChannelEnabled, constant.ErrorLogEnabled
	common.AutomaticDisableChannelEnabled, constant.ErrorLogEnabled = true, false
	t.Cleanup(func() {
		common.AutomaticDisableChannelEnabled, constant.ErrorLogEnabled = oldEnabled, oldLogs
		service.ClearChannelHealth(308)
	})
	service.ClearChannelHealth(308)
	info := &relaycommon.RelayInfo{OriginModelName: "gpt-6-astra", IsStream: true, RelayFormat: types.RelayFormatOpenAIResponses}
	err := types.NewOpenAIError(errors.New("upstream overloaded"), types.ErrorCodeBadResponse, 502)
	for i := 0; i < 4; i++ {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx, cancel := context.WithCancel(context.Background())
		c.Request = httptest.NewRequest("POST", "/v1/responses", nil).WithContext(ctx)
		a := &hedgeAttempt{role: relaycommon.HedgeRoleHedge, c: c, info: info, channel: &model.Channel{Id: 308, AutoBan: intPointer(1)}, err: err}
		accountBackupError(a)
		cancel()
		require.True(t, a.errorAccounted)
	}
	action, _ := service.EvaluateChannelHealth(types.ChannelError{ChannelId: 308, AutoBan: true}, err, relayProbeTarget(info))
	require.Equal(t, service.HealthProbeBeforeDisable, action)
}

func intPointer(v int) *int { return &v }

func TestBackupFailureRecordedBeforeLaterCancellationExactlyOnce(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil).WithContext(ctx)
	a := &hedgeAttempt{role: relaycommon.HedgeRoleHedge, c: c, err: types.NewOpenAIError(errors.New("overloaded"), types.ErrorCodeBadResponse, 502)}
	n := 0
	a.recordBackupError(func() { n++ })
	require.Equal(t, 1, n)
	a.recordBackupError(func() { n++ })
	require.Equal(t, 1, n)
	cancel()
	a.recordBackupError(func() { n++ })
	require.Equal(t, 1, n)
	require.True(t, a.errorAccounted)
}

func TestCanceledBackupAndPrimaryDoNotGetBackupAccounting(t *testing.T) {
	for _, role := range []string{relaycommon.HedgeRolePrimary, relaycommon.HedgeRoleHedge} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c.Request = httptest.NewRequest("POST", "/v1/responses", nil).WithContext(ctx)
		a := &hedgeAttempt{role: role, c: c, err: types.NewError(errors.New("canceled"), types.ErrorCodeDoRequestFailed)}
		a.recordBackupError(func() { t.Fatal("canceled attempts must not affect health") })
		require.False(t, a.errorAccounted)
	}
	require.False(t, commonAutoReenableShouldProbeModel(commonAutoDisabledModel{CooldownUntil: common.GetTimestamp() + 300}))
	require.True(t, commonAutoReenableShouldProbeModel(commonAutoDisabledModel{CooldownUntil: time.Now().Add(-time.Minute).Unix()}))
}
