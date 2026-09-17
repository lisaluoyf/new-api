package service

import (
	"context"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

func TestValidateRelayStreamEndRequiresTerminalUsageForCanceledSettlement(t *testing.T) {
	for _, reason := range []relaycommon.StreamEndReason{relaycommon.StreamEndReasonClientGone, relaycommon.StreamEndReasonScannerErr} {
		s := relaycommon.NewStreamStatus()
		s.SetEndReason(reason, context.Canceled)
		info := &relaycommon.RelayInfo{}
		require.NotNil(t, ValidateRelayStreamEnd(nil, info, s, true, true))
		s.RecordTerminalUsage("response.completed", "resp_test")
		require.Nil(t, ValidateRelayStreamEnd(nil, info, s, true, true))
		require.NotNil(t, ValidateRelayStreamEnd(nil, info, s, true, false))
		require.NotNil(t, ValidateRelayStreamEnd(nil, info, s, false, true))
		s.RecordError("malformed upstream event")
		require.NotNil(t, ValidateRelayStreamEnd(nil, info, s, true, true))
	}
}

func TestValidateRelayStreamEndRejectsHTTP200WithoutUsableOutput(t *testing.T) {
	info := &relaycommon.RelayInfo{}

	for _, endReason := range []relaycommon.StreamEndReason{
		relaycommon.StreamEndReasonDone,
		relaycommon.StreamEndReasonEOF,
	} {
		t.Run(string(endReason), func(t *testing.T) {
			status := relaycommon.NewStreamStatus()
			status.SetEndReason(endReason, nil)
			err := ValidateRelayStreamEnd(nil, info, status, true, false)
			require.NotNil(t, err)
			require.Equal(t, types.ErrorCodeEmptyResponse, err.GetErrorCode())
		})
	}
}

func TestValidateRelayStreamEndRejectsLifecycleOnlyStream(t *testing.T) {
	info := &relaycommon.RelayInfo{}
	status := relaycommon.NewStreamStatus()
	status.SetEndReason(relaycommon.StreamEndReasonDone, nil)

	err := ValidateRelayStreamEnd(nil, info, status, false, false)
	require.NotNil(t, err)
	require.Equal(t, types.ErrorCodeBadResponse, err.GetErrorCode())
}

func TestValidateRelayStreamEndAcceptsCompletedUsableOutput(t *testing.T) {
	info := &relaycommon.RelayInfo{}
	for _, endReason := range []relaycommon.StreamEndReason{
		relaycommon.StreamEndReasonDone,
		relaycommon.StreamEndReasonEOF,
	} {
		status := relaycommon.NewStreamStatus()
		status.SetEndReason(endReason, nil)
		require.Nil(t, ValidateRelayStreamEnd(nil, info, status, true, true))
	}
}
