package controller

import (
	"errors"
	"net/http"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

func TestImageChannelExhaustionReturnsRetryablePublicError(t *testing.T) {
	routeErr := types.NewError(errors.New("private routing group and provider"), types.ErrorCodeGetChannelFailed)
	prior := types.NewOpenAIError(errors.New("private provider failed"), types.ErrorCodeBadResponseStatusCode, 524)
	for _, mode := range []int{relayconstant.RelayModeImagesEdits, relayconstant.RelayModeImagesGenerations} {
		info := &relaycommon.RelayInfo{RelayMode: mode, LastError: prior}
		got := imageChannelExhaustedError(info, routeErr)
		require.Equal(t, http.StatusServiceUnavailable, got.StatusCode)
		require.Equal(t, types.ErrorCode("image_channels_exhausted"), got.GetErrorCode())
		require.True(t, types.IsSkipRetryError(got))
		require.NotContains(t, got.ToOpenAIError().Message, "private")
		require.Same(t, prior, info.LastError)
		info.LastError = nil
		require.Same(t, routeErr, imageChannelExhaustedError(info, routeErr))
	}
	require.Same(t, routeErr, imageChannelExhaustedError(&relaycommon.RelayInfo{LastError: prior}, routeErr))
	paramErr := types.NewError(errors.New("bad parameter"), types.ErrorCodeInvalidRequest)
	require.Same(t, paramErr, imageChannelExhaustedError(&relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesEdits, LastError: prior}, paramErr))
}
