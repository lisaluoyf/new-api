package service

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

var videoErrorIdentifier = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

// Classification improves the message only. A wrapped 5xx still does not prove
// whether the provider accepted a generation, and cannot authorize another POST.
func publicVideoMaterialRejection(status int, body []byte) (string, string, bool) {
	if !gjson.ValidBytes(body) {
		return "", "", false
	}
	code := gjson.GetBytes(body, "error.code").String()
	if status == 409 && code == "ASSET_CERTIFICATION_REQUIRED" {
		return "asset_certification_required", "Reference material is not certified for this channel; complete material certification before generating", true
	}
	if status == 502 && code == "PROVIDER_REQUEST_REJECTED" {
		message := strings.ToLower(gjson.GetBytes(body, "error.message").String())
		if strings.Contains(message, "may be related to copyright restrictions") {
			return "content_policy_violation", "The input material was rejected by the upstream copyright review", true
		}
		if strings.Contains(message, "may contain sensitive information") {
			return "content_policy_violation", "The input material was rejected by the upstream content safety review", true
		}
	}
	return "", "", false
}

func RememberVideoSubmitErrorReference(c *gin.Context, body []byte) {
	for _, path := range []string{"error.details.taskId", "error.details.task_id"} {
		ref := gjson.GetBytes(body, path).String()
		if videoErrorIdentifier.MatchString(ref) && model.SanitizeTaskFailure(ref, common.GetContextKeyString(c, constant.ContextKeyChannelKey)) == ref {
			c.Set("video_submit_error_reference", ref)
			return
		}
	}
}

func RecordVideoSubmissionFailure(c *gin.Context, taskErr *dto.TaskError) {
	record := VideoSubmissionFromContext(c)
	if record == nil || taskErr == nil {
		return
	}
	public := PublicTaskErrorView(c, taskErr)
	code := public.Code
	if !videoErrorIdentifier.MatchString(code) {
		code = "video_service_error"
	}
	message := model.SanitizeTaskFailure(public.Message, common.GetContextKeyString(c, constant.ContextKeyChannelKey))
	diagnostic := ""
	if public.UpstreamError != nil {
		raw, _ := common.Marshal(public.UpstreamError)
		diagnostic = string(raw)
	}
	if err := model.RecordVideoSubmissionError(record.ID, record.OwnerToken, taskErr.StatusCode, code, message, c.GetString("video_submit_error_reference"), diagnostic); err != nil {
		common.SysError("video submission failure evidence could not be saved")
	} else {
		c.Set("video_submission_error_saved", true)
	}
}

type videoSubmissionErrorWriter struct {
	gin.ResponseWriter
	body bytes.Buffer
}

func (w *videoSubmissionErrorWriter) Write(data []byte) (int, error) {
	if n := 4096 - w.body.Len(); n > 0 {
		if n > len(data) {
			n = len(data)
		}
		w.body.Write(data[:n])
	}
	return w.ResponseWriter.Write(data)
}

func (w *videoSubmissionErrorWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

// Observe local middleware failures too, without buffering or changing the
// actual response. Only a bounded, sanitized code/message is persisted.
func ObserveVideoSubmissionFailure(c *gin.Context) func() {
	w := &videoSubmissionErrorWriter{ResponseWriter: c.Writer}
	c.Writer = w
	return func() {
		if w.Status() < 400 || c.GetBool("video_submission_error_saved") {
			return
		}
		code, message := "", ""
		for _, path := range []string{"error.code", "code", "error.type"} {
			if code = gjson.GetBytes(w.body.Bytes(), path).String(); code != "" {
				break
			}
		}
		for _, path := range []string{"error.message", "message"} {
			if message = gjson.GetBytes(w.body.Bytes(), path).String(); message != "" {
				break
			}
		}
		if message == "" {
			message = "Video service request failed"
		}
		RecordVideoSubmissionFailure(c, &dto.TaskError{StatusCode: w.Status(), Code: code, Message: message})
	}
}

func VideoSubmissionPublicError(record *model.VideoSubmission) map[string]any {
	if record.ErrorCode == "" || record.Status == model.VideoSubmissionAccepted {
		return nil
	}
	values := model.TaskErrorRedactionValues(record.ChannelID, "", record.Model)
	result := map[string]any{"code": common.PublicDiagnosticCode(record.ErrorCode, values...), "message": common.SanitizeUpstreamErrorText(model.SanitizeTaskFailure(record.ErrorMessage), values...), "status_code": record.ErrorStatus}
	var detail common.PublicUpstreamError
	if record.ErrorDetails != "" && common.UnmarshalJsonStr(record.ErrorDetails, &detail) == nil {
		result["upstream_error"] = common.RedactPublicUpstreamError(&detail, values...)
	}
	return result
}
