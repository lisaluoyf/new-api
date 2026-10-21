package model

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"gorm.io/gorm"
)

// PublicTaskError keeps stable code/message semantics and adds complete,
// redacted upstream failure diagnostics for every task model and channel.
func PublicTaskError(task *Task) *dto.OpenAIVideoError {
	return publicTaskErrorWithDB(DB, task)
}

func PublicTaskFailure(task *Task) string {
	if e := PublicTaskError(task); e != nil {
		return e.Message
	}
	return ""
}

func publicTaskErrorWithDB(db *gorm.DB, task *Task) *dto.OpenAIVideoError {
	if task == nil || task.Status != TaskStatusFailure {
		return nil
	}
	if db != nil {
		db = db.Session(&gorm.Session{NewDB: true})
	}
	sensitive := taskFailureSensitiveValues(db, task)
	code := "task_failed"
	if NormalizeVerifiedVideoModel(task.Properties.OriginModelName) != "" {
		if explicit, _ := taskFailureDetails(task); explicit != "" {
			code = explicit
		}
	}
	if parts := strings.SplitN(code, ".", 2); len(parts) == 2 && sanitizeTaskFailure(parts[1], sensitive...) != parts[1] {
		code = "task_failed"
	}
	result := &dto.OpenAIVideoError{Code: code, Message: sanitizeTaskFailure(taskFailureReason(task), sensitive...)}
	// All asynchronous adapters already persist the provider response in Data.
	// Never expose a successful response or stale first-route diagnostics.
	fallbackFailed := task.PrivateData.SeedanceFallback != nil && task.PrivateData.SeedanceFallback.Started && task.PrivateData.SeedanceFallback.Stage == "failed"
	diagnostic := map[string]any{"message": taskFailureReason(task)}
	if !fallbackFailed {
		if response := common.FailureDiagnostics(task.Data); response != nil {
			diagnostic["response"] = response
		}
	}
	raw, _ := common.Marshal(diagnostic)
	result.UpstreamError = common.NewPublicUpstreamError(0, raw, nil, sensitive...)
	result.Message = common.SanitizeUpstreamErrorText(result.Message, result.UpstreamError.RedactionValues()...)
	result.Code = common.PublicDiagnosticCode(result.Code, result.UpstreamError.RedactionValues()...)
	return result
}

func SanitizeTaskFailure(reason string, sensitive ...string) string {
	return sanitizeTaskFailure(reason, sensitive...)
}

func TaskErrorMessage(value map[string]interface{}, depth int) string {
	return taskErrorMessage(value, depth)
}

// A code is public only if it belongs to a known error family. This preserves
// official subcodes without allowing keys, URLs or diagnostics in error.code.
var publicVideoCodePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z][A-Za-z0-9_]*)*$`)

func PublicVideoErrorCode(code string) string {
	if len(code) > 128 || !publicVideoCodePattern.MatchString(code) {
		return ""
	}
	root := strings.SplitN(code, ".", 2)[0]
	switch root {
	case "InputTextSensitiveContentDetected", "InputImageSensitiveContentDetected", "InputVideoSensitiveContentDetected", "InputAudioSensitiveContentDetected",
		"OutputTextSensitiveContentDetected", "OutputImageSensitiveContentDetected", "OutputVideoSensitiveContentDetected", "OutputAudioSensitiveContentDetected",
		"InputTextRiskDetection", "InputImageRiskDetection", "InputVideoRiskDetection", "InputAudioRiskDetection",
		"OutputTextRiskDetection", "OutputImageRiskDetection", "OutputVideoRiskDetection", "OutputAudioRiskDetection",
		"InvalidParameter", "InvalidParameterValue", "MissingParameter", "MissingParameterValue", "UnsupportedParameter",
		"ResourceNotFound", "ResourceUnavailable", "LimitExceeded", "QuotaExceeded", "Throttling", "TooManyRequests", "InternalError", "ServiceUnavailable", "TaskExpired",
		"nsfw_content_detected", "invalid_asset_material", "content_policy_violation", "invalid_request", "invalid_request_error", "rate_limit_exceeded", "video_service_error", "task_failed", "generation_failed", "upstream_material_api_failed":
		return code
	}
	return ""
}

func findPublicVideoError(value map[string]any, depth int) (string, string) {
	if depth > 8 {
		return "", ""
	}
	// The nested upstream error is more specific than a provider's outer wrapper.
	for _, key := range []string{"upstream", "error", "response", "data"} {
		if child, ok := value[key].(map[string]any); ok {
			if code, message := findPublicVideoError(child, depth+1); code != "" {
				return code, message
			}
		}
	}
	code, _ := value["code"].(string)
	if code = PublicVideoErrorCode(code); code == "" {
		code, _ = value["type"].(string)
		code = PublicVideoErrorCode(code)
	}
	if code == "" {
		return "", ""
	}
	message, _ := value["message"].(string)
	if (strings.Contains(code, "SensitiveContentDetected") || strings.Contains(code, "RiskDetection")) && !isTaskReviewRejection(message) {
		// Keep the classification, but do not surface unrelated nested diagnostics.
		message = ""
	}
	return code, message
}

func taskFailureDetails(task *Task) (string, string) {
	// Some compatible channels put an explicit official code in fail_reason
	// rather than in an error object. This is parsing, never a guess from prose.
	textCode, textMessage := taskFailureTextDetails(task.FailReason)
	// A failed material preparation/submission may still carry the first route's
	// response. It must never replace the final recovery failure with stale data.
	fallbackFailed := task.PrivateData.SeedanceFallback != nil && task.PrivateData.SeedanceFallback.Started && task.PrivateData.SeedanceFallback.Stage == "failed"
	if !fallbackFailed {
		if task.Platform == "videofee" {
			if code, message := VideoFeeFailureDetails(task.Data); code != "" {
				if textCode != "" && (code == "task_failed" || code == "upstream_material_api_failed") {
					return textCode, textMessage
				}
				return code, message
			}
		}
		var data map[string]any
		if common.Unmarshal(task.Data, &data) == nil {
			if code, message := findPublicVideoError(data, 0); code != "" && code != "task_failed" && code != "generation_failed" {
				if textCode != "" && (code == "nsfw_content_detected" || code == "invalid_request") {
					return textCode, textMessage
				}
				return code, message
			}
		}
	}
	if textCode != "" {
		return textCode, textMessage
	}
	if index := strings.Index(task.FailReason, "{"); index >= 0 {
		var data map[string]any
		if common.UnmarshalJsonStr(task.FailReason[index:], &data) == nil {
			if code, message := findPublicVideoError(data, 0); code != "" {
				return code, message
			}
		}
	}
	switch task.FailReason {
	case "Backup channel material review timed out":
		return "asset_review_timeout", task.FailReason
	case "Backup channel submission could not be confirmed; no further submission was made":
		return "submission_outcome_unknown", task.FailReason
	case "Backup channel submission failed":
		return "video_service_error", task.FailReason
	}
	return "", ""
}

func taskFailureTextDetails(reason string) (string, string) {
	reason = strings.TrimSpace(reason)
	if index := strings.Index(reason, ":"); index > 0 && index <= 128 {
		if code := PublicVideoErrorCode(strings.TrimSpace(reason[:index])); code != "" {
			return code, strings.TrimSpace(reason[index+1:])
		}
	}
	return "", ""
}

func collectTaskAssetIDs(value any, ids *[]string, depth int) {
	if depth > 32 {
		return
	}
	switch v := value.(type) {
	case string:
		if strings.HasPrefix(v, "asset://") {
			*ids = append(*ids, strings.TrimPrefix(v, "asset://"))
		}
	case []any:
		for _, child := range v {
			collectTaskAssetIDs(child, ids, depth+1)
		}
	case map[string]any:
		for _, child := range v {
			collectTaskAssetIDs(child, ids, depth+1)
		}
	}
}

var taskErrorCredential = regexp.MustCompile(`(?i)(?:authorization|proxy-authorization|x-api-key|api[_-]?key|key|x-secret|token|session|signature|access[_-]?token|refresh[_-]?token|password|secret|cookie|set-cookie)["']?\s*[:=]\s*(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\r\n,;]+)`)
var taskErrorBearer = regexp.MustCompile(`(?i)\b(?:Bearer|Basic)\s+[A-Za-z0-9._~+/=-]+`)
var taskErrorKey = regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]+|AIza[A-Za-z0-9_-]+|eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)\b`)
var taskErrorURL = regexp.MustCompile(`(?i)(?:https?|wss?|s3|gs|asset)://[^\s<>"']+`)
var taskErrorIPv6 = regexp.MustCompile(`(?i)(?:\[[0-9a-f]*:[0-9a-f:]+\]|(?:[0-9a-f]{1,4}:){2,}[0-9a-f:]+)`)
var taskErrorEmail = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
var taskErrorDiagnostic = regexp.MustCompile(`(?i)(?:\b(?:request|transaction|trace|channel|merchant|account|asset|group)[_ -]?id\b["']?\s*[:=]\s*[^\s,;]+|(?:/var/|/opt/|/home/|/root/|/app/)[^\s"']+)`)
var taskReviewRequestID = regexp.MustCompile(`(?i)^request[_ -]?id\s*[:=]\s*[a-z0-9][a-z0-9_-]{11,159}$`)

// The transaction supplied by the webhook outbox must also be used for these
// lookups, including on single-connection SQLite databases.
func taskFailureSensitiveValues(db *gorm.DB, task *Task) []string {
	sensitive := []string{task.PrivateData.Key, task.PrivateData.UpstreamTaskID, task.PrivateData.HedgeUpstreamTaskID}
	if task.Properties.UpstreamModelName != task.Properties.OriginModelName {
		sensitive = append(sensitive, task.Properties.UpstreamModelName)
	}
	if task.ChannelId > 0 && db != nil && db.Migrator().HasTable(&Channel{}) {
		var channel Channel
		if err := db.Select("name", "key", "base_url", "type").Where("id = ?", task.ChannelId).First(&channel).Error; err == nil {
			sensitive = append(sensitive, channel.Name, channel.Key)
			sensitive = append(sensitive, common.UpstreamIdentityValues(channel.Name, channel.GetBaseURL(), task.Properties.UpstreamModelName, task.Properties.OriginModelName)...)
			for _, part := range strings.FieldsFunc(channel.Name, func(r rune) bool { return r == '_' || r == '-' || unicode.IsSpace(r) }) {
				if len(part) > 3 {
					sensitive = append(sensitive, part)
				}
			}
			for _, key := range strings.Split(channel.Key, "\n") {
				sensitive = append(sensitive, strings.TrimSpace(key))
			}
		}
	}
	if db != nil && strings.Contains(task.PrivateData.RequestData, "asset://") && db.Migrator().HasTable(&SeedanceResource{}) {
		var fields map[string]any
		if common.UnmarshalJsonStr(task.PrivateData.RequestData, &fields) == nil {
			ids := []string{}
			collectTaskAssetIDs(fields, &ids, 0)
			if len(ids) > 0 {
				var assets []SeedanceResource
				// Keep historical failures safe even after a material is deleted.
				if db.Unscoped().Where("user_id = ? AND kind = ? AND id IN ?", task.UserId, "asset", ids).Find(&assets).Error == nil {
					for _, asset := range assets {
						sensitive = append(sensitive, asset.UpstreamID)
					}
				}
			}
		}
	}
	return sensitive
}

// Apimart wraps the actionable review message in data.error.upstream while
// keeping only a generic content-safety message at the outer level. Read the
// saved response so historical tasks and their logs benefit without rewriting
// the original audit or billing records.
func taskFailureReason(task *Task) string {
	if task.PrivateData.SeedanceFallback != nil && task.PrivateData.SeedanceFallback.Started && task.PrivateData.SeedanceFallback.Stage == "failed" {
		return task.FailReason
	}
	if NormalizeVerifiedVideoModel(task.Properties.OriginModelName) != "" {
		_, message := taskFailureDetails(task)
		if message != "" && !IsGenericTaskFailureReason(message) {
			return message
		}
	}
	var envelope map[string]any
	if task.Platform == "apimart-video" && common.Unmarshal(task.Data, &envelope) == nil {
		data, _ := envelope["data"].(map[string]any)
		err, _ := data["error"].(map[string]any)
		upstream, _ := err["upstream"].(map[string]any)
		message, _ := upstream["message"].(string)
		code, _ := upstream["code"].(string)
		if strings.HasSuffix(code, ".PolicyViolation") && isTaskReviewRejection(message) {
			return message
		}
	}
	return task.FailReason
}

func isTaskReviewRejection(reason string) bool {
	reason = strings.ToLower(reason)
	for _, marker := range []string{
		"may be related to copyright restrictions", "may contain sensitive information",
		"did not pass content safety review", "rejected by the safety system",
		"rejected as a result of our safety system", "content policy violation",
		"may contain real person", "sensitive content", "contentsecuritydetection",
	} {
		if strings.Contains(reason, marker) {
			return true
		}
	}
	return false
}

func sanitizeTaskFailure(reason string, sensitive ...string) string {
	reason = strings.TrimSpace(reason)
	// Some adapters retain the JSON response envelope. Return only its message,
	// never headers, credentials, request bodies or other diagnostic siblings.
	if index := strings.Index(reason, "{"); index >= 0 {
		var envelope map[string]interface{}
		if err := common.UnmarshalJsonStr(reason[index:], &envelope); err != nil {
			return "Task failed"
		}
		reason = taskErrorMessage(envelope, 0)
	}
	for _, value := range sensitive {
		if value = strings.TrimSpace(value); value != "" {
			reason = regexp.MustCompile("(?i)"+regexp.QuoteMeta(value)).ReplaceAllString(reason, "[redacted]")
		}
	}
	reason = taskErrorCredential.ReplaceAllString(reason, "[redacted]")
	reason = taskErrorBearer.ReplaceAllString(reason, "[redacted]")
	reason = taskErrorKey.ReplaceAllString(reason, "[redacted]")
	reason = taskErrorURL.ReplaceAllString(reason, "[redacted]")
	reason = taskErrorEmail.ReplaceAllString(reason, "[redacted]")
	reason = taskErrorIPv6.ReplaceAllString(reason, "[redacted]")
	// Review request IDs are useful when asking the provider to investigate a
	// false positive. Only retain bounded opaque IDs in explicit review errors;
	// credentials and URLs have already been removed above.
	review := isTaskReviewRejection(reason)
	reason = taskErrorDiagnostic.ReplaceAllStringFunc(reason, func(diagnostic string) string {
		if review && taskReviewRequestID.MatchString(diagnostic) {
			return diagnostic
		}
		return "[redacted]"
	})
	reason = common.MaskSensitiveInfo(reason)
	reason = strings.Join(strings.FieldsFunc(reason, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }), " ")
	if strings.TrimSpace(strings.ReplaceAll(reason, "[redacted]", "")) == "" {
		return "Task failed"
	}
	runes := []rune(reason)
	if len(runes) > 1000 {
		reason = string(runes[:1000]) + "…"
	}
	return reason
}

func taskErrorMessage(value map[string]interface{}, depth int) string {
	if depth > 4 {
		return ""
	}
	for _, key := range []string{"error", "response", "data"} {
		if nested, ok := value[key].(map[string]interface{}); ok {
			if text := taskErrorMessage(nested, depth+1); text != "" {
				return text
			}
		}
	}
	for _, key := range []string{"message", "fail_reason", "reason", "error_message", "error"} {
		if text, ok := value[key].(string); ok && strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}

func TaskErrorRedactionValues(channelID int, key, publicModel string) []string {
	return taskFailureSensitiveValues(DB, &Task{ChannelId: channelID, Properties: Properties{OriginModelName: publicModel, UpstreamModelName: publicModel}, PrivateData: TaskPrivateData{Key: key}})
}
