package service

import (
	"fmt"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/tidwall/gjson"
)

func SeedanceCompletionTokens(task *model.Task) (int64, bool) {
	if task == nil || task.Properties.OriginModelName != "seedance-2.5" || task.Status != model.TaskStatusSuccess {
		return 0, false
	}
	for _, p := range []string{"data.usage.completion_tokens", "usage.completion_tokens"} {
		v := gjson.GetBytes(task.Data, p)
		if v.Exists() && v.Type == gjson.Number && v.Int() >= 0 {
			return v.Int(), true
		}
	}
	return 0, false
}

func SeedanceLastFrameSource(task *model.Task) string {
	if task == nil || task.Status != model.TaskStatusSuccess || task.PrivateData.SeedanceRequest["return_last_frame"] != true {
		return ""
	}
	for _, p := range []string{"data.result.videos.0.last_frame_url", "result.videos.0.last_frame_url", "data.result.last_frame_url"} {
		if u := gjson.GetBytes(task.Data, p).String(); IsValidMediaResultURL(u) {
			return u
		}
	}
	return ""
}

func AddSeedanceResultFields(task *model.Task, out map[string]any) {
	if task == nil || task.Properties.OriginModelName != "seedance-2.5" || task.Status != model.TaskStatusSuccess {
		return
	}
	if task.FinishTime >= task.SubmitTime && task.SubmitTime > 0 {
		out["actual_time"] = task.FinishTime - task.SubmitTime
	}
	if tokens, ok := SeedanceCompletionTokens(task); ok {
		out["usage"] = map[string]any{"completion_tokens": tokens}
	}
	if SeedanceLastFrameSource(task) != "" {
		out["last_frame_url"] = fmt.Sprintf("%s/v1/videos/%s/last-frame", system_setting.ServerAddress, task.TaskID)
	}
}

// Historical completed tasks may predate usage logging. Query-time backfill
// updates statistics only; it never changes wallet quota or settlement.
func SyncSeedanceUsageLog(task *model.Task) error {
	if model.LOG_DB == nil {
		return nil
	}
	if tokens, ok := SeedanceCompletionTokens(task); ok {
		return model.UpdateLogResultByTaskID(task.UserId, task.TaskID, 0, map[string]any{"usage": map[string]any{"completion_tokens": tokens}})
	}
	return nil
}

func SeedanceCompatibleTaskResponse(task *model.Task) map[string]any {
	status := "processing"
	switch task.Status {
	case model.TaskStatusSuccess:
		status = "succeeded"
	case model.TaskStatusFailure:
		status = "failed"
	case model.TaskStatusQueued, model.TaskStatusSubmitted, model.TaskStatusNotStart:
		status = "queued"
	}
	format := "mp4"
	if task.PrivateData.SeedanceRequest["output_format"] == "mov" {
		format = "mov"
	}
	out := map[string]any{"error": nil, "format": format, "metadata": nil, "status": status, "task_id": task.TaskID, "url": ""}
	if task.Status == model.TaskStatusSuccess && task.GetResultURL() != "" {
		out["url"] = fmt.Sprintf("%s/v1/videos/%s/content", system_setting.ServerAddress, task.TaskID)
	}
	if task.Status == model.TaskStatusFailure {
		out["error"] = map[string]any{"code": "task_failed", "message": PublicTaskFailure(task)}
	}
	AddSeedanceResultFields(task, out)
	return map[string]any{"code": "success", "message": "", "data": out}
}
