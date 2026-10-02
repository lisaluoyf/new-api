package service

import (
	"fmt"
	"math"
	"strconv"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/tidwall/gjson"
)

func SeedanceCompletionTokens(task *model.Task) (int64, bool) {
	if task == nil || !IsSeedanceLibraryModel(task.Properties.OriginModelName) || task.Status != model.TaskStatusSuccess {
		return 0, false
	}
	for _, p := range []string{"data.usage.completion_tokens", "usage.completion_tokens", "data.data.usage.completion_tokens"} {
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
	paths := []string{"data.result.videos.0.last_frame_url", "result.videos.0.last_frame_url", "data.result.last_frame_url"}
	if task.ChannelId == constant.VideoFeeSeedanceChannelID || task.ChannelId == constant.BeeNexSeedanceChannelID {
		paths = append(paths, "data.last_frame_url", "data.last_frame.url", "data.data.last_frame_url", "data.data.content.last_frame_url", "last_frame_url")
	}
	for _, p := range paths {
		if u := gjson.GetBytes(task.Data, p).String(); IsValidMediaResultURL(u) {
			return u
		}
	}
	return ""
}

func AddSeedanceResultFields(task *model.Task, out map[string]any) {
	AddSeedanceBillingReceipt(task, out)
	if task == nil || !IsSeedanceLibraryModel(task.Properties.OriginModelName) || task.Status != model.TaskStatusSuccess {
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

// Reference duration is measured before charging; output duration is obtained
// from the completed task, with the explicit request as a historical fallback.
func Seedance20VariantBillableSeconds(task *model.Task) int {
	if task == nil || !IsSeedance20Variant(task.Properties.OriginModelName) {
		return 0
	}
	output := 0
	for _, path := range []string{"data.output_duration", "output_duration", "data.duration", "duration", "data.result.videos.0.duration", "result.videos.0.duration"} {
		if v := gjson.GetBytes(task.Data, path); v.Exists() && v.Float() > 0 {
			output = int(math.Round(v.Float()))
			break
		}
	}
	if output <= 0 {
		output = seedanceInt(task.PrivateData.SeedanceRequest["duration"])
	}
	if output <= 0 {
		output = 5
	}
	return output + seedanceInt(task.PrivateData.SeedanceRequest["video_input_seconds"])
}
func seedanceInt(value any) int { n, _ := strconv.Atoi(fmt.Sprint(value)); return n }
