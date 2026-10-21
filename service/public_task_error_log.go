package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// Logging is independent of channel-health/retry attribution: a local/material
// rejection must still leave the customer a complete error record.
func RecordPublicTaskErrorLog(c *gin.Context, failure *dto.TaskError) {
	if failure == nil || failure.ErrorLogRecorded || !constant.ErrorLogEnabled || c.GetInt("id") <= 0 || model.LOG_DB == nil {
		return
	}
	public := PublicTaskErrorView(c, failure)
	other := map[string]any{"error_code": public.Code, "status_code": public.StatusCode}
	content := common.SanitizeUpstreamErrorText(public.Message, common.GetContextKeyString(c, constant.ContextKeyChannelKey))
	if public.UpstreamError != nil {
		other["upstream_error"] = public.UpstreamError
		raw, _ := common.Marshal(public.UpstreamError)
		content += " | upstream_error: " + string(raw)
	}
	if record := VideoSubmissionFromContext(c); record != nil {
		other["task_id"] = record.TaskID
	}
	if err := model.RecordErrorLog(c, c.GetInt("id"), c.GetInt("channel_id"), c.GetString("original_model"), c.GetString("token_name"), content, c.GetInt("token_id"), 0, false, c.GetString("group"), other); err == nil {
		failure.ErrorLogRecorded = true
	}
}
