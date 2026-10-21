package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

func PublicErrorRedactionValues(c *gin.Context) []string {
	if c == nil {
		return nil
	}
	values := []string{common.GetContextKeyString(c, constant.ContextKeyChannelKey)}
	return append(values, common.UpstreamIdentityValues(c.GetString("channel_name"), c.GetString("base_url"), c.GetString("public_error_private_model"), c.GetString("original_model"))...)
}

// A public view must never mutate the error used by billing, retry or health.
func PublicTaskErrorView(c *gin.Context, failure *dto.TaskError) *dto.TaskError {
	if failure == nil {
		return nil
	}
	view := *failure
	values := append(PublicErrorRedactionValues(c), failure.UpstreamError.RedactionValues()...)
	view.UpstreamError = common.RedactPublicUpstreamError(failure.UpstreamError, values...)
	view.Code = common.PublicDiagnosticCode(view.Code, values...)
	view.Message = common.SanitizeUpstreamErrorText(view.Message, values...)
	if view.Data != nil {
		raw, _ := common.Marshal(view.Data)
		view.Data = common.NewPublicUpstreamError(0, raw, nil, values...).Body
	}
	return &view
}
func PublicRelayErrorView(c *gin.Context, failure *types.NewAPIError) *types.NewAPIError {
	if failure == nil {
		return nil
	}
	view := *failure
	view.PublicRedactionValues = append(PublicErrorRedactionValues(c), failure.UpstreamError.RedactionValues()...)
	view.UpstreamError = common.RedactPublicUpstreamError(failure.UpstreamError, view.PublicRedactionValues...)
	return &view
}
