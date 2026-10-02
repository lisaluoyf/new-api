package model

import (
	"database/sql/driver"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// TEXT values are strings with PostgreSQL/MySQL and may be []byte with SQLite.
// Implement Scanner/Valuer explicitly rather than relying on json.RawMessage.
type SeedanceReceiptJSON []byte

func (j *SeedanceReceiptJSON) Scan(value any) error {
	switch v := value.(type) {
	case string:
		*j = append((*j)[:0], v...)
	case []byte:
		*j = append((*j)[:0], v...)
	case nil:
		*j = nil
	default:
		return fmt.Errorf("unsupported receipt JSON database type %T", value)
	}
	return nil
}

func (j SeedanceReceiptJSON) Value() (driver.Value, error) {
	if len(j) == 0 {
		return nil, nil
	}
	return string(j), nil
}

type SeedanceInputVideoMeasurement struct {
	Index           int      `json:"index"`
	MediaID         string   `json:"media_id"`
	MeasuredSeconds *float64 `json:"measured_seconds"`
	BillableSeconds int      `json:"billable_seconds"`
	DurationSource  string   `json:"duration_source"`
}

type SeedanceOutputDuration struct {
	ActualSeconds   *float64 `json:"actual_seconds"`
	BillableSeconds int      `json:"billable_seconds"`
	Source          string   `json:"duration_source"`
}

type SeedanceBillingDetails struct {
	SchemaVersion               string                          `json:"schema_version"`
	ReceiptID                   string                          `json:"receipt_id,omitempty"`
	TaskID                      string                          `json:"task_id,omitempty"`
	SettlementStatus            string                          `json:"settlement_status"`
	Model                       string                          `json:"model"`
	Resolution                  string                          `json:"resolution"`
	TariffVariant               string                          `json:"tariff_variant"`
	TariffRevision              string                          `json:"tariff_revision"`
	Currency                    string                          `json:"currency"`
	Unit                        string                          `json:"unit"`
	BaseUnitRate                string                          `json:"base_unit_rate"`
	ChannelMultiplier           float64                         `json:"channel_multiplier"`
	AccountMultiplier           float64                         `json:"account_multiplier"`
	EffectiveUnitRate           string                          `json:"effective_unit_rate"`
	RequestedOutputSeconds      int                             `json:"requested_output_seconds"`
	EstimatedOutputSeconds      int                             `json:"estimated_output_seconds"`
	EstimatedBillableSeconds    int                             `json:"estimated_billable_seconds"`
	InputVideos                 []SeedanceInputVideoMeasurement `json:"input_videos"`
	BillableInputVideoSeconds   int                             `json:"billable_input_video_seconds"`
	Output                      *SeedanceOutputDuration         `json:"output"`
	BillableSeconds             *int                            `json:"billable_seconds"`
	Rounding                    map[string]string               `json:"rounding"`
	EstimatedAmount             string                          `json:"estimated_amount"`
	ReservedAmount              string                          `json:"reserved_amount"`
	AdditionalDebitAmount       string                          `json:"additional_debit_amount"`
	FinalDebitAmount            *string                         `json:"final_debit_amount"`
	RefundedAmount              string                          `json:"refunded_amount"`
	NetAmount                   *string                         `json:"net_amount"`
	InitialQuotaUnits           int                             `json:"initial_quota_units"`
	FinalQuotaUnits             *int                            `json:"final_quota_units"`
	AccountUnitUSD              string                          `json:"account_unit_usd"`
	MediaCharges                map[string]string               `json:"media_charges"`
	FinalDebitMayExceedEstimate bool                            `json:"final_debit_may_exceed_estimate"`
	CreatedAt                   int64                           `json:"created_at"`
	SettledAt                   *int64                          `json:"settled_at"`
}

// SeedanceBillingReceipt stores a public, URL-free billing snapshot separately
// from task polling data, so provider responses cannot overwrite the receipt.
type SeedanceBillingReceipt struct {
	ID           string `gorm:"primaryKey;size:191"`
	TaskID       string `gorm:"uniqueIndex;size:191"`
	UserID       int    `gorm:"index"`
	Status       string `gorm:"size:32;index:idx_sd_receipt_pending,priority:1"`
	InitialQuota int
	FinalQuota   int
	Details      SeedanceReceiptJSON `gorm:"type:text"`
	CreatedAt    int64
	UpdatedAt    int64 `gorm:"index:idx_sd_receipt_pending,priority:2"`
}

func GetSeedanceBillingReceipt(userID int, taskID string) (*SeedanceBillingReceipt, error) {
	var receipt SeedanceBillingReceipt
	err := DB.Where("user_id = ? AND task_id = ?", userID, taskID).First(&receipt).Error
	return &receipt, err
}

func SaveSeedanceBillingReceipt(receipt *SeedanceBillingReceipt, details any) error {
	encoded, err := common.Marshal(details)
	if err != nil {
		return err
	}
	receipt.Details = encoded
	receipt.UpdatedAt = time.Now().Unix()
	return DB.Model(&SeedanceBillingReceipt{}).Where("id = ? AND status = ?", receipt.ID, "pending").Updates(map[string]any{
		"details": receipt.Details, "status": receipt.Status, "final_quota": receipt.FinalQuota, "updated_at": receipt.UpdatedAt,
	}).Error
}

func InsertSeedanceTaskWithReceipt(task *Task, receipt *SeedanceBillingReceipt) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(task).Error; err != nil {
			return err
		}
		return tx.Create(receipt).Error
	})
}

// Only completed tasks whose receipt was not finalized are recovery candidates.
func PendingCompletedSeedanceReceipts(limit int) ([]SeedanceBillingReceipt, error) {
	var receipts []SeedanceBillingReceipt
	err := DB.Model(&SeedanceBillingReceipt{}).Select("seedance_billing_receipts.*").
		Joins("JOIN tasks ON tasks.task_id = seedance_billing_receipts.task_id AND tasks.user_id = seedance_billing_receipts.user_id").
		Where("seedance_billing_receipts.status = ? AND tasks.status IN ? AND tasks.finish_time > 0 AND tasks.finish_time < ?", "pending", []TaskStatus{TaskStatusSuccess, TaskStatusFailure}, time.Now().Unix()-60).
		Order("seedance_billing_receipts.updated_at").Limit(limit).Find(&receipts).Error
	return receipts, err
}

// Match only the top-level task ID, never a referenced task in request_data.
func SeedanceTaskLedgerNet(userID int, taskID string) (int, bool, error) {
	var rows []Log
	if LOG_DB == nil {
		return 0, false, fmt.Errorf("billing log database unavailable")
	}
	err := LOG_DB.Where("user_id = ? AND type IN ? AND other LIKE ?", userID, []int{LogTypeConsume, LogTypeRefund}, "%"+taskID+"%").Find(&rows).Error
	if err != nil {
		return 0, false, err
	}
	net, found := 0, false
	for _, row := range rows {
		var other map[string]any
		if common.UnmarshalJsonStr(row.Other, &other) != nil || other["task_id"] != taskID {
			continue
		}
		found = true
		if row.Type == LogTypeConsume {
			net += row.Quota
		} else {
			net -= row.Quota
		}
	}
	return net, found, nil
}
