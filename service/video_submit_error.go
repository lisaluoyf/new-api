package service

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"net/http"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
)

// Stable public summaries retain existing retry and health semantics. Complete,
// redacted upstream details are attached independently for every error status.
func PublicVideoSubmitError(status int, body []byte, key string, channelID int) (result *dto.TaskError) {
	defer func() {
		if result != nil {
			result.UpstreamError = common.NewPublicUpstreamError(status, body, nil, key)
		}
	}()
	if code, message, ok := publicVideoMaterialRejection(status, body); ok {
		return TaskErrorWrapperLocal(fmt.Errorf("%s", message), code, status)
	}

	if status != http.StatusBadRequest && status != http.StatusUnprocessableEntity && status != http.StatusTooManyRequests {
		return TaskErrorWrapper(fmt.Errorf("Video service request failed"), "video_service_error", status)
	}
	public := PublicTaskError(&model.Task{Status: model.TaskStatusFailure, ChannelId: channelID, FailReason: string(body), Data: body, Properties: model.Properties{OriginModelName: "seedance-2.5"}, PrivateData: model.TaskPrivateData{Key: key}})
	code, message := public.Code, public.Message
	if status == http.StatusTooManyRequests {
		if code == "task_failed" {
			code = "rate_limit_exceeded"
		}
		if message == "" || message == "Task failed" || public.Code == "task_failed" {
			message = "Request rate limit reached; retry later"
		}
		return TaskErrorWrapper(fmt.Errorf("%s", message), code, status)
	}
	if code == "task_failed" {
		code = "invalid_request"
	}
	if message == "" || message == "Task failed" {
		message = "Invalid video generation request"
	}
	return TaskErrorWrapperLocal(fmt.Errorf("%s", message), code, status)
}
