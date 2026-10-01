package service

import (
	"fmt"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/tidwall/gjson"
)

// Only actionable client errors pass through the same public task redaction.
// Server failures remain generic and no provider envelope is forwarded.
func PublicVideoSubmitError(status int, body []byte, key string, channelID int) *dto.TaskError {
	if status != 400 {
		return TaskErrorWrapper(fmt.Errorf("Video service request failed"), "video_service_error", status)
	}
	code := "invalid_request"
	for _, p := range []string{"error.code", "error.type"} {
		switch gjson.GetBytes(body, p).String() {
		case "nsfw_content_detected":
			code = "nsfw_content_detected"
		case "invalid_asset_material":
			code = "invalid_asset_material"
		}
	}
	message := PublicTaskFailure(&model.Task{Status: model.TaskStatusFailure, ChannelId: channelID, FailReason: string(body), PrivateData: model.TaskPrivateData{Key: key}})
	if message == "" || message == "Task failed" {
		message = "Invalid video generation request"
	}
	return TaskErrorWrapperLocal(fmt.Errorf("%s", message), code, status)
}
