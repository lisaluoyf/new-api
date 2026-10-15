package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"strings"
)

// Shared with the transactional webhook outbox so every public task response
// uses the same error code, message and redaction rules.
func PublicTaskFailure(task *model.Task) string              { return model.PublicTaskFailure(task) }
func PublicTaskError(task *model.Task) *dto.OpenAIVideoError { return model.PublicTaskError(task) }
func sanitizeTaskFailure(reason string, sensitive ...string) string {
	return model.SanitizeTaskFailure(reason, sensitive...)
}
func taskErrorMessage(value map[string]interface{}, depth int) string {
	return model.TaskErrorMessage(value, depth)
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
				if model.NormalizeVerifiedVideoModel(task.Properties.OriginModelName) != "" {
					other["task_fail_code"] = PublicTaskError(task).Code
				}
				log.Content = "Task failed: " + reason
			} else {
				delete(other, "task_fail_reason")
				delete(other, "task_fail_code")
			}
		}
		log.Other = common.MapToJsonStr(other)
	}
}
