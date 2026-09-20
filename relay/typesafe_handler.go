package relay

import (
	"bytes"
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

func TypeSafeHelper(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	info.InitChannelMeta(c)
	if info.ChannelType != constant.ChannelTypeTypeSafe {
		return types.NewErrorWithStatusCode(fmt.Errorf("/v1/systemone requires a TypeSafe channel"), types.ErrorCodeInvalidApiType, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	request, ok := info.Request.(*dto.TypeSafeRequest)
	if !ok {
		return types.NewError(fmt.Errorf("invalid TypeSafe request"), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	copy, err := common.DeepCopy(request)
	if err != nil {
		return types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	if err = helper.ModelMappedHelper(c, info, copy); err != nil {
		return types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}
	copy.Stream = nil // stream is a gateway validation field, not a native TypeSafe parameter.
	data, err := common.Marshal(copy)
	if err != nil {
		return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	if len(info.ParamOverride) > 0 {
		data, err = relaycommon.ApplyParamOverrideWithRelayInfo(data, info)
		if err != nil {
			return newAPIErrorFromParamOverride(err)
		}
	}
	adaptor := GetAdaptor(info.ApiType)
	adaptor.Init(info)
	response, err := adaptor.DoRequest(c, info, bytes.NewReader(data))
	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusBadGateway)
	}
	resp := response.(*http.Response)
	if resp.StatusCode != http.StatusOK {
		if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
			c.Header("Retry-After", retryAfter)
		}
		apiErr := service.RelayErrorHandler(c.Request.Context(), resp, false)
		service.ResetStatusCode(apiErr, c.GetString("status_code_mapping"))
		return apiErr
	}
	usage, apiErr := adaptor.DoResponse(c, resp, info)
	if apiErr != nil {
		return apiErr
	}
	service.PostTextConsumeQuota(c, info, usage.(*dto.Usage), nil)
	return nil
}
