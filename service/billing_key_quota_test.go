package service

import (
	"errors"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

func TestWalletPreConsumeKeyQuotaFailureIsForbiddenAndAtomic(t *testing.T) {
	truncate(t)
	seedUser(t, 1, 1000)
	seedToken(t, 2, 1, "retry-billing-key", 10)
	info := retryBillingInfo(1000)
	session := &BillingSession{relayInfo: info, funding: &WalletFunding{userId: 1}}
	apiErr := session.preConsume(retryBillingContext(), 100)
	require.NotNil(t, apiErr)
	require.Equal(t, http.StatusForbidden, apiErr.StatusCode)
	require.Equal(t, types.ErrorCodePreConsumeTokenQuotaFailed, apiErr.GetErrorCode())
	require.True(t, types.IsSkipRetryError(apiErr))
	require.False(t, types.IsRecordErrorLog(apiErr))
	key, _ := apiErr.GetClientMessage()
	require.Equal(t, i18n.MsgTokenQuotaInsufficient, key)
	require.Zero(t, session.GetPreConsumedQuota())
	require.False(t, session.NeedsRefund())
	var user model.User
	var token model.Token
	require.NoError(t, model.DB.First(&user, 1).Error)
	require.NoError(t, model.DB.First(&token, 2).Error)
	require.Equal(t, 1000, user.Quota)
	require.Equal(t, 10, token.RemainQuota)
	require.Zero(t, token.UsedQuota)
}

func TestFallbackKeyQuotaFailurePreservesOriginalReservation(t *testing.T) {
	truncate(t)
	seedUser(t, 1, 1000)
	seedToken(t, 2, 1, "retry-billing-key", 150)
	info := retryBillingInfo(1000)
	ctx := retryBillingContext()
	require.Nil(t, PreConsumeBilling(ctx, 100, info))
	apiErr := PrepareRetryBilling(ctx, info, types.PriceData{QuotaToPreConsume: 200})
	require.NotNil(t, apiErr)
	require.Equal(t, http.StatusForbidden, apiErr.StatusCode)
	require.Equal(t, types.ErrorCodePreConsumeTokenQuotaFailed, apiErr.GetErrorCode())
	require.Equal(t, 100, info.Billing.GetPreConsumedQuota())
	var user model.User
	var token model.Token
	require.NoError(t, model.DB.First(&user, 1).Error)
	require.NoError(t, model.DB.First(&token, 2).Error)
	require.Equal(t, 900, user.Quota)
	require.Equal(t, 50, token.RemainQuota)
}

func TestWalletQuotaAdjustmentErrorPreservesUnexpectedFailures(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   types.ErrorCode
	}{
		{model.ErrInsufficientWalletQuota, http.StatusForbidden, types.ErrorCodeInsufficientUserQuota},
		{model.ErrInsufficientTokenQuota, http.StatusForbidden, types.ErrorCodePreConsumeTokenQuotaFailed},
		{errors.New("token not found"), http.StatusInternalServerError, types.ErrorCodeUpdateDataError},
		{errors.New("database unavailable"), http.StatusInternalServerError, types.ErrorCodeUpdateDataError},
	} {
		apiErr := walletQuotaAdjustmentAPIError(tc.err)
		require.Equal(t, tc.status, apiErr.StatusCode)
		require.Equal(t, tc.code, apiErr.GetErrorCode())
		require.Equal(t, tc.status == http.StatusInternalServerError, types.IsRecordErrorLog(apiErr))
	}
}
