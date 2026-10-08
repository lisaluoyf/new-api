package model

import (
	"github.com/QuantumNous/new-api/common"
)

func UpdateTaskDiscountAccounting(userID int, taskID string, quota int, groupRatio float64) error {
	row, err := findConsumeLogRowForTask(userID, taskID)
	if err != nil {
		return err
	}
	if row.AccountingStatus == AccountingStatusRefunded || groupRatio <= 0 {
		return nil
	}
	finalAmount := float64(quota) / common.QuotaPerUnit
	priceAmount := finalAmount / groupRatio
	updates := map[string]interface{}{
		"accounting_user_final_amount_usd": finalAmount,
		"accounting_user_price_amount_usd": priceAmount,
	}
	snapshot, _ := common.StrToMap(row.AccountingSnapshot)
	if snapshot != nil {
		amounts, _ := snapshot["amounts_usd"].(map[string]interface{})
		if amounts == nil {
			amounts = map[string]interface{}{}
		}
		amounts["user_final"] = finalAmount
		amounts["user_price"] = priceAmount
		snapshot["amounts_usd"] = amounts
		snapshot["quota"] = quota
		updates["accounting_snapshot"] = common.MapToJsonStr(snapshot)
	}
	return LOG_DB.Model(&Log{}).Where("id = ? AND accounting_status <> ?", row.Id, AccountingStatusRefunded).Updates(updates).Error
}
