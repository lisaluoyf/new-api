package model

import (
	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// A closed provider execution is evidence for the existing hold refund worker;
// it is not a new refund or a claim that separate material costs were zero.
func VideoFeeFencedHoldRejection(hold *BillingHold) bool {
	if DB == nil || hold == nil || hold.ChannelId != 273 || hold.RequestId == "" {
		return false
	}
	var rows []VideoSubmission
	if DB.Where("user_id = ? AND request_id = ? AND channel_id = ? AND provider_lookup_enabled = ?", hold.UserId, hold.RequestId, 273, true).Limit(2).Find(&rows).Error != nil || len(rows) != 1 {
		return false
	}
	var o VideoFeeSubmissionObservation
	return common.UnmarshalJsonStr(string(rows[0].ProviderObservation), &o) == nil && o.Acceptance == "not_accepted" && o.NewSubmissionSafe && o.TaskID == nil
}

func videoFeeFinancialReady(tx *gorm.DB, row *VideoSubmission, snapshot *VideoSubmissionRecovery) (bool, error) {
	if row.ProviderRefundCompleted || snapshot.Task.Quota == 0 {
		return true, nil
	}
	var refund VideoSubmissionRefund
	err := tx.Where("id = ? AND user_id = ? AND task_id = ?", row.ID, row.UserID, row.TaskID).First(&refund).Error
	if err == nil {
		return refund.WalletQuota == snapshot.Task.Quota, nil
	}
	if err != gorm.ErrRecordNotFound {
		return false, err
	}
	var count int64
	err = tx.Model(&BillingHold{}).Where("request_id = ? AND user_id = ? AND status = ? AND pre_consumed_quota = ?", row.RequestID, row.UserID, BillingHoldStatusRefunded, snapshot.Task.Quota).Count(&count).Error
	return count == 1, err
}

// Called after a non-wallet session has successfully refunded both its original
// funding source and token reservation. A crash before this checkpoint remains
// unresolved, rather than authorizing another credit or a new generation.
func CompleteVideoSubmissionRefund(userID int, taskID string) error {
	return DB.Model(&VideoSubmission{}).Where("user_id = ? AND task_id = ? AND channel_id = ? AND provider_lookup_enabled = ? AND recovery_settled = ? AND provider_refund_completed = ?", userID, taskID, 273, true, false, false).Updates(map[string]any{"provider_refund_completed": true, "provider_refund_recorded_at": common.GetTimestamp()}).Error
}

func videoFeePublishBilling(tx *gorm.DB, row *VideoSubmission, snapshot *VideoSubmissionRecovery, refunded bool) error {
	if refunded || !row.RecoverySettled {
		return nil
	}
	var existing int64
	if err := tx.Model(&SeedanceBillingReceipt{}).Where("task_id = ?", row.TaskID).Count(&existing).Error; err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}
	r := snapshot.Receipt
	if r == nil || r.TaskID != row.TaskID || r.UserID != row.UserID || r.InitialQuota != snapshot.Task.Quota {
		return ErrVideoSubmissionClaimLost
	}
	var details SeedanceBillingDetails
	if common.Unmarshal(r.Details, &details) != nil || details.TaskID != row.TaskID || details.Model != row.Model {
		return ErrVideoSubmissionClaimLost
	}
	details.SettlementStatus = "requires_review"
	raw, err := common.Marshal(details)
	if err != nil {
		return err
	}
	r.Details = raw
	r.Status = "requires_review"
	return tx.Create(r).Error
}
