package service

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

var taskErrorCredential = regexp.MustCompile(`(?i)(?:authorization|proxy-authorization|x-api-key|api[_-]?key|key|x-secret|token|session|signature|access[_-]?token|refresh[_-]?token|password|secret|cookie|set-cookie)["']?\s*[:=]\s*(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\r\n,;]+)`)
var taskErrorBearer = regexp.MustCompile(`(?i)\b(?:Bearer|Basic)\s+[A-Za-z0-9._~+/=-]+`)
var taskErrorKey = regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]+|AIza[A-Za-z0-9_-]+|eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)\b`)
var taskErrorURL = regexp.MustCompile(`(?i)(?:https?|wss?|s3|gs)://[^\s<>"']+`)
var taskErrorIPv6 = regexp.MustCompile(`(?i)(?:\[[0-9a-f:]+\]|(?:[0-9a-f]{1,4}:){2,}[0-9a-f:]+)`)
var taskErrorEmail = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
var taskErrorDiagnostic = regexp.MustCompile(`(?i)(?:\b(?:request|transaction|trace|channel|merchant|account)[_-]?id\b["']?\s*[:=]\s*[^\s,;]+|(?:/var/|/opt/|/home/|/root/|/app/)[^\s"']+)`)

// PublicTaskFailure preserves useful errors while removing provider credentials,
// routing and identifiers. Original task/audit records are never modified.
func PublicTaskFailure(task *model.Task) string {
	if task == nil || task.Status != model.TaskStatusFailure {
		return ""
	}
	sensitive := []string{task.PrivateData.Key, task.PrivateData.UpstreamTaskID, task.PrivateData.HedgeUpstreamTaskID}
	if task.Properties.UpstreamModelName != task.Properties.OriginModelName {
		sensitive = append(sensitive, task.Properties.UpstreamModelName)
	}
	if task.ChannelId > 0 && model.DB != nil {
		if channel, err := model.GetChannelById(task.ChannelId, true); err == nil && channel != nil {
			sensitive = append(sensitive, channel.Name, channel.Key)
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
	return sanitizeTaskFailure(task.FailReason, sensitive...)
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
	reason = taskErrorDiagnostic.ReplaceAllString(reason, "[redacted]")
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

// EnrichUserTaskLogFailures uses ownership-checked task records at read time so
// historical failures are immediately useful without rewriting billing logs.
func EnrichUserTaskLogFailures(logs []*model.Log) {
	for _, log := range logs {
		if log == nil {
			continue
		}
		other, _ := common.StrToMap(log.Other)
		if other == nil {
			continue
		}
		// Never trust raw diagnostic fields already persisted by older adapters.
		for _, field := range []string{"task_fail_reason", "task_fail_code"} {
			if text, ok := other[field].(string); ok {
				other[field] = sanitizeTaskFailure(text)
			}
		}
		id, ok := other["task_id"].(string)
		if ok && strings.TrimSpace(id) != "" {
			if task, exists, err := model.GetByOnlyTaskId(id); err == nil && exists && task.UserId == log.UserId && task.Status == model.TaskStatusFailure {
				reason := PublicTaskFailure(task)
				other["task_fail_reason"] = reason
				log.Content = "Task failed: " + reason
			} else {
				delete(other, "task_fail_reason")
				delete(other, "task_fail_code")
			}
		}
		log.Other = common.MapToJsonStr(other)
	}
}
