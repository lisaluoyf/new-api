package service

import (
	"context"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/tidwall/gjson"
)

// SyncOmniTaskAccounting replaces submission estimates on the original task
// charge. Quota adjustment logs remain an immutable funding audit trail.
func SyncOmniTaskAccounting(task *model.Task, result *relaycommon.TaskInfo) error {
	if task == nil || task.Platform != "apimart-omni" || task.Status != model.TaskStatusSuccess {
		return nil
	}
	bc := task.PrivateData.BillingContext
	if bc == nil || bc.GroupRatio <= 0 {
		return nil
	}
	cost := 0.
	for _, p := range []string{"data.cost", "cost"} {
		if n := gjson.GetBytes(task.Data, p).Float(); n > 0 {
			cost = n
			break
		}
	}
	if cost <= 0 {
		cost = gjson.GetBytes(task.Data, "data.credits_cost").Float() / 10
	}
	var rows []model.Log
	if e := model.LOG_DB.Where("user_id = ? AND type = ? AND model_name = ? AND other LIKE ?", task.UserId, model.LogTypeConsume, "gemini-omni-1.1-flash", "%"+task.TaskID+"%").Order("id ASC").Limit(30).Find(&rows).Error; e != nil {
		return e
	}
	for _, row := range rows {
		var other, snap map[string]any
		if common.UnmarshalJsonStr(row.Other, &other) != nil || other["is_task"] != true || other["task_id"] != task.TaskID {
			continue
		}
		if common.UnmarshalJsonStr(row.AccountingSnapshot, &snap) != nil || snap == nil {
			return fmt.Errorf("missing Omni accounting snapshot")
		}
		final := float64(task.Quota) / common.QuotaPerUnit
		amounts, ok := snap["amounts_usd"].(map[string]any)
		if !ok {
			amounts = map[string]any{}
			snap["amounts_usd"] = amounts
		}
		amounts["user_final"], amounts["user_price"] = final, final/bc.GroupRatio
		snap["settled_quota"], snap["billing_unit"] = task.Quota, "second"
		other["final_amount_usd"], other["user_amount_usd"], other["media_type"], other["billing_unit"] = final, final/bc.GroupRatio, "video", "second"
		update := map[string]any{"accounting_user_final_amount_usd": final, "accounting_user_price_amount_usd": final / bc.GroupRatio}
		if cost > 0 {
			amounts["channel_cost"] = cost
			snap["channel_cost_source"] = "provider_task_cost"
			other["upstream_actual_cost_usd"], other["proc_amount_usd"], other["proc_source"] = cost, cost, "provider_task_cost"
			update["accounting_channel_cost_amount_usd"] = cost
			// These estimates are fully superseded by the provider receipt and settled
			// user ledger. Do not erase unrelated accounting errors.
			if snap["error"] == nil || snap["error"] == "" || snap["error"] == "roma_user_price_missing" || snap["error"] == "video_procurement_variant_missing" {
				snap["status"], other["accounting_status"] = "ok", "ok"
				delete(snap, "error")
				delete(other, "accounting_error")
				update["accounting_status"] = "ok"
			}
		} else {
			snap["status"], other["accounting_status"] = "partial", "partial"
			snap["error"] = "omni_final_provider_cost_missing"
			update["accounting_status"] = "partial"
		}
		seconds := 0.
		for _, p := range []string{"data.output_duration", "data.duration", "data.result.duration"} {
			if n := gjson.GetBytes(task.Data, p).Float(); n > 0 {
				seconds = n
				break
			}
		}

		if seconds <= 0 && result != nil && result.Url != "" {
			n, e := ProbeRemoteVideoDurationSecondsRound(context.Background(), result.Url)
			if e == nil {
				seconds = float64(n)
			}
		}
		if seconds > 0 {
			other["output_video_seconds"], other["duration_seconds"] = seconds, seconds
			if rd, ok := other["request_data"].(map[string]any); ok {
				rd["output_duration"] = seconds
			}
		}
		if task.GetResultURL() != "" {
			other["result_url"] = task.GetResultURL()
		}
		update["other"], update["accounting_snapshot"] = common.MapToJsonStr(other), common.MapToJsonStr(snap)
		if e := model.LOG_DB.Model(&model.Log{}).Where("id = ? AND other = ? AND accounting_snapshot = ?", row.Id, row.Other, row.AccountingSnapshot).Updates(update).Error; e != nil {
			return e
		}

		return nil
	}
	return fmt.Errorf("original Omni consume log missing")
}
