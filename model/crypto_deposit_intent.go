package model

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	CryptoDepositIntentStatusPending   = "pending"
	CryptoDepositIntentStatusConfirmed = "confirmed"
	CryptoDepositIntentStatusFailed    = "failed"
)

type CryptoDepositIntent struct {
	Id                string  `json:"id" gorm:"primaryKey;type:varchar(64)"`
	UserId            int     `json:"user_id" gorm:"not null;index"`
	Chain             string  `json:"chain" gorm:"type:varchar(32);not null;index;uniqueIndex:uk_crypto_deposit_chain_tx,priority:1"`
	TokenSymbol       string  `json:"token_symbol" gorm:"type:varchar(16);not null"`
	TokenAddress      string  `json:"token_address" gorm:"type:varchar(64);default:''"`
	WalletAddressFrom string  `json:"wallet_address_from" gorm:"type:varchar(64);not null;index"`
	ExpectedToAddress string  `json:"expected_to_address" gorm:"type:varchar(64);not null"`
	Challenge         string  `json:"challenge" gorm:"type:text;not null"`
	WalletSignature   string  `json:"wallet_signature" gorm:"type:text;default:''"`
	TxHash            *string `json:"tx_hash,omitempty" gorm:"type:varchar(80);uniqueIndex:uk_crypto_deposit_chain_tx,priority:2"`
	Status            string  `json:"status" gorm:"type:varchar(32);not null;default:'pending';index"`
	UsdAdded          float64 `json:"usd_added" gorm:"type:decimal(18,6);default:0"`
	ErrorMessage      string  `json:"error_message" gorm:"type:text"`
	ExpiresAt         int64   `json:"expires_at" gorm:"bigint;not null;index"`
	VerifiedAt        int64   `json:"verified_at" gorm:"bigint;default:0"`
	ConfirmedAt       int64   `json:"confirmed_at" gorm:"bigint;default:0"`
	CreatedAt         int64   `json:"created_at" gorm:"bigint;index"`
	UpdatedAt         int64   `json:"updated_at" gorm:"bigint;index"`
}

func (intent *CryptoDepositIntent) BeforeCreate(_ *gorm.DB) error {
	now := common.GetTimestamp()
	if strings.TrimSpace(intent.Id) == "" {
		intent.Id = common.GetUUID()
	}
	intent.Chain = strings.ToLower(strings.TrimSpace(intent.Chain))
	intent.TokenSymbol = strings.ToUpper(strings.TrimSpace(intent.TokenSymbol))
	intent.TokenAddress = strings.ToLower(strings.TrimSpace(intent.TokenAddress))
	intent.WalletAddressFrom = strings.ToLower(strings.TrimSpace(intent.WalletAddressFrom))
	intent.ExpectedToAddress = strings.ToLower(strings.TrimSpace(intent.ExpectedToAddress))
	if intent.TxHash != nil {
		txHash := strings.ToLower(strings.TrimSpace(*intent.TxHash))
		intent.TxHash = &txHash
	}
	if strings.TrimSpace(intent.Status) == "" {
		intent.Status = CryptoDepositIntentStatusPending
	}
	intent.CreatedAt = now
	intent.UpdatedAt = now
	return nil
}

func (intent *CryptoDepositIntent) BeforeUpdate(_ *gorm.DB) error {
	intent.Chain = strings.ToLower(strings.TrimSpace(intent.Chain))
	intent.TokenSymbol = strings.ToUpper(strings.TrimSpace(intent.TokenSymbol))
	intent.TokenAddress = strings.ToLower(strings.TrimSpace(intent.TokenAddress))
	intent.WalletAddressFrom = strings.ToLower(strings.TrimSpace(intent.WalletAddressFrom))
	intent.ExpectedToAddress = strings.ToLower(strings.TrimSpace(intent.ExpectedToAddress))
	if intent.TxHash != nil {
		txHash := strings.ToLower(strings.TrimSpace(*intent.TxHash))
		intent.TxHash = &txHash
	}
	intent.UpdatedAt = common.GetTimestamp()
	return nil
}
