package service

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/tidwall/gjson"
)

// Reuse the frozen procurement unit rate when output duration is confirmed.
// Legacy in-flight tasks can recover their variant from their request snapshot.
func backfillSeedanceEstimatedCost(task *model.Task, seconds int) error {
	if task == nil || seconds <= 0 || task.Status != model.TaskStatusSuccess {
		return nil
	}
	row, err := model.FindConsumeLogRowForTask(task.UserId, task.TaskID)
	if err != nil {
		return err
	}
	if row.AccountingStatus != "ok" {
		return nil
	}
	var other map[string]any
	if err := common.UnmarshalJsonStr(row.Other, &other); err != nil {
		return err
	}
	if value, ok := other["upstream_actual_cost_usd"].(float64); ok && value > 0 {
		return nil
	}
	var snap map[string]any
	if err := common.UnmarshalJsonStr(row.AccountingSnapshot, &snap); err != nil {
		return err
	}
	prices, ok := snap["prices"].(map[string]any)
	if !ok {
		return fmt.Errorf("missing procurement prices")
	}
	cost, ok := prices["channel_cost"].(map[string]any)
	if !ok {
		return fmt.Errorf("missing procurement unit rate")
	}
	rate, ok := cost["input_price"].(float64)
	if !ok || rate <= 0 {
		return fmt.Errorf("invalid procurement unit rate")
	}
	if snap["cost_price_source"] != "configured_video_variant_estimate" {
		variant, _ := task.PrivateData.SeedanceRequest["billing_variant"].(string)
		if strings.TrimSpace(variant) == "" {
			return fmt.Errorf("missing frozen billing variant")
		}
		ratio := ratio_setting.GetVideoModelOfficialPriceRatio(taskModelName(task), variant)
		if ratio <= 0 {
			return fmt.Errorf("missing procurement variant price")
		}
		rate *= ratio
		cost["input_price"] = rate
		snap["duration_price_variant"], snap["duration_price_ratio"] = variant, ratio
	}
	snap["cost_price_source"] = "configured_video_variant_estimate"
	units, ok := snap["units"].(map[string]any)
	if !ok {
		units = map[string]any{}
		snap["units"] = units
	}
	units["duration_seconds"] = seconds
	amounts, ok := snap["amounts_usd"].(map[string]any)
	if !ok {
		return fmt.Errorf("missing accounting amounts")
	}
	amounts["channel_cost"] = rate * float64(seconds)
	// Preserve captured provider currency without guessing a USD exchange rate.
	for _, path := range []string{"data.billing", "billing"} {
		receipt := gjson.GetBytes(task.Data, path)
		if receipt.Get("reservation_status").String() == "captured" && receipt.Get("charged_amount").Float() > 0 {
			snap["upstream_captured_charge"] = map[string]any{"currency": receipt.Get("currency").String(), "amount": receipt.Get("charged_amount").String()}
			break
		}
	}
	encoded, err := common.Marshal(snap)
	if err != nil {
		return err
	}
	// Do not overwrite a concurrent actual-cost receipt, refund, or settlement.
	return model.LOG_DB.Model(&model.Log{}).Where("id = ? AND accounting_status = ? AND accounting_snapshot = ? AND other = ?", row.Id, "ok", row.AccountingSnapshot, row.Other).
		Updates(map[string]any{"accounting_channel_cost_amount_usd": rate * float64(seconds), "accounting_snapshot": string(encoded)}).Error
}
