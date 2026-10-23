package model

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"time"
)

// VideoFeeHoldRefundEvidence permits customer refund from a validated original
// rejection or a terminal failed original with an exactly released reservation.
// It does not assert that the external supplier's final charge is zero.
func VideoFeeHoldRefundEvidence(hold *BillingHold) bool {
	if DB == nil || hold == nil || hold.ChannelId != 273 || hold.RequestId == "" || hold.PreConsumedQuota <= 0 {
		return false
	}
	var rows []VideoSubmission
	if DB.Where("user_id = ? AND request_id = ? AND channel_id = ? AND provider_lookup_enabled = ?", hold.UserId, hold.RequestId, 273, true).Limit(2).Find(&rows).Error != nil || len(rows) != 1 {
		return false
	}
	row := rows[0]
	var o VideoFeeSubmissionObservation
	if common.UnmarshalJsonStr(string(row.ProviderObservation), &o) != nil || o.Validate("apimaster-"+row.TaskID, time.Now()) != nil {
		return false
	}
	var snapshot VideoSubmissionRecovery
	if common.UnmarshalJsonStr(row.RecoveryJSON, &snapshot) != nil || snapshot.Task.TaskID != row.TaskID || snapshot.Task.UserId != hold.UserId || snapshot.Task.ChannelId != 273 || snapshot.Task.Quota != hold.PreConsumedQuota {
		return false
	}
	if row.UpstreamTaskID != "" && (o.TaskID == nil || row.UpstreamTaskID != *o.TaskID) {
		return false
	}
	if o.RejectedWithRetryPermission() {
		return true
	}
	if o.Acceptance != "accepted" || o.GenerationStatus() != "failed" || o.Billing.Reservation != "released" || len(o.Billing.Charge.Entries) != 0 || len(o.Billing.Refund.Entries) != 0 {
		return false
	}
	if o.Billing.Hold.Status != "recorded" || o.Billing.Release.Status != "recorded" || len(o.Billing.Hold.Entries) == 0 || len(o.Billing.Release.Entries) == 0 {
		return false
	}
	sum := func(entries []VideoFeeLedgerEntry) (decimal.Decimal, decimal.Decimal) {
		credit, yuan := decimal.Zero, decimal.Zero
		for _, e := range entries {
			c, _ := decimal.NewFromString(e.Credit)
			y, _ := decimal.NewFromString(e.Yuan)
			credit = credit.Add(c)
			yuan = yuan.Add(y)
		}
		return credit, yuan
	}
	hc, hy := sum(o.Billing.Hold.Entries)
	rc, ry := sum(o.Billing.Release.Entries)
	return hc.IsPositive() && hy.IsPositive() && hc.Equal(rc) && hy.Equal(ry)
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
