package model

import (
	"encoding/json"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

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
	Status       string `gorm:"size:32"`
	InitialQuota int
	FinalQuota   int
	Details      json.RawMessage `gorm:"type:text"`
	CreatedAt    int64
	UpdatedAt    int64
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
