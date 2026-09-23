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
	CryptoDepositIntentStatusExpired   = "expired"
)

type CryptoDepositIntent struct {
	Id                       string  `json:"id" gorm:"primaryKey;type:varchar(64)"`
	UserId                   int     `json:"user_id" gorm:"not null;index"`
	Chain                    string  `json:"chain" gorm:"type:varchar(32);not null;index;uniqueIndex:uk_crypto_deposit_chain_tx,priority:1"`
	TokenSymbol              string  `json:"token_symbol" gorm:"type:varchar(16);not null"`
	TokenAddress             string  `json:"token_address" gorm:"type:varchar(64);default:''"`
	WalletAddressFrom        string  `json:"wallet_address_from" gorm:"type:varchar(64);not null;index"`
	ExpectedToAddress        string  `json:"expected_to_address" gorm:"type:varchar(64);not null"`
	Challenge                string  `json:"challenge" gorm:"type:text;not null"`
	WalletSignature          string  `json:"wallet_signature" gorm:"type:text;default:''"`
	TxHash                   *string `json:"tx_hash,omitempty" gorm:"type:varchar(128);uniqueIndex:uk_crypto_deposit_chain_tx,priority:2"`
	TopUpTradeNo             string  `json:"topup_trade_no" gorm:"type:varchar(255);default:'';index"`
	Purpose                  string  `json:"purpose" gorm:"type:varchar(32);not null;default:'wallet_topup';index"`
	SubscriptionOrderTradeNo string  `json:"subscription_order_trade_no" gorm:"type:varchar(255);default:'';index"`
	ExpectedUsdAmount        float64 `json:"expected_usd_amount" gorm:"type:decimal(18,6);not null;default:0"`
	CreditUsdAmount          float64 `json:"credit_usd_amount" gorm:"type:decimal(18,6);not null;default:0"`
	AssetUsdPrice            float64 `json:"asset_usd_price" gorm:"type:decimal(24,10);not null;default:0"`
	ExpectedBaseUnits        string  `json:"expected_base_units" gorm:"type:varchar(128);not null;default:''"`
	Status                   string  `json:"status" gorm:"type:varchar(32);not null;default:'pending';index"`
	UsdAdded                 float64 `json:"usd_added" gorm:"type:decimal(18,6);default:0"`
	ErrorMessage             string  `json:"error_message" gorm:"type:text"`
	ExpiresAt                int64   `json:"expires_at" gorm:"bigint;not null;index"`
	RecoveryExpiresAt        int64   `json:"recovery_expires_at" gorm:"bigint;not null;default:0;index"`
	AuthorizedAt             int64   `json:"authorized_at" gorm:"bigint;not null;default:0;index"`
	TxSubmittedAt            int64   `json:"tx_submitted_at" gorm:"bigint;not null;default:0"`
	NextCheckAt              int64   `json:"next_check_at" gorm:"bigint;not null;default:0;index"`
	LastCheckedAt            int64   `json:"last_checked_at" gorm:"bigint;not null;default:0"`
	RetryCount               int     `json:"retry_count" gorm:"not null;default:0"`
	VerifiedAt               int64   `json:"verified_at" gorm:"bigint;default:0"`
	ConfirmedAt              int64   `json:"confirmed_at" gorm:"bigint;default:0"`
	CreatedAt                int64   `json:"created_at" gorm:"bigint;index"`
	UpdatedAt                int64   `json:"updated_at" gorm:"bigint;index"`
}

func (intent *CryptoDepositIntent) BeforeCreate(_ *gorm.DB) error {
	now := common.GetTimestamp()
	if strings.TrimSpace(intent.Id) == "" {
		intent.Id = common.GetUUID()
	}
	intent.Chain = strings.ToLower(strings.TrimSpace(intent.Chain))
	intent.TokenSymbol = strings.ToUpper(strings.TrimSpace(intent.TokenSymbol))
	intent.TokenAddress = normalizeCryptoAddress(intent.Chain, intent.TokenAddress)
	intent.WalletAddressFrom = normalizeCryptoAddress(intent.Chain, intent.WalletAddressFrom)
	intent.ExpectedToAddress = normalizeCryptoAddress(intent.Chain, intent.ExpectedToAddress)
	intent.Purpose = strings.ToLower(strings.TrimSpace(intent.Purpose))
	if intent.Purpose == "" {
		intent.Purpose = "wallet_topup"
	}
	intent.SubscriptionOrderTradeNo = strings.TrimSpace(intent.SubscriptionOrderTradeNo)
	intent.TopUpTradeNo = strings.TrimSpace(intent.TopUpTradeNo)
	intent.ExpectedBaseUnits = strings.TrimSpace(intent.ExpectedBaseUnits)
	if intent.TxHash != nil {
		txHash := normalizeCryptoTxHash(intent.Chain, *intent.TxHash)
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
	intent.TokenAddress = normalizeCryptoAddress(intent.Chain, intent.TokenAddress)
	intent.WalletAddressFrom = normalizeCryptoAddress(intent.Chain, intent.WalletAddressFrom)
	intent.ExpectedToAddress = normalizeCryptoAddress(intent.Chain, intent.ExpectedToAddress)
	intent.Purpose = strings.ToLower(strings.TrimSpace(intent.Purpose))
	intent.SubscriptionOrderTradeNo = strings.TrimSpace(intent.SubscriptionOrderTradeNo)
	intent.TopUpTradeNo = strings.TrimSpace(intent.TopUpTradeNo)
	intent.ExpectedBaseUnits = strings.TrimSpace(intent.ExpectedBaseUnits)
	if intent.TxHash != nil {
		txHash := normalizeCryptoTxHash(intent.Chain, *intent.TxHash)
		intent.TxHash = &txHash
	}
	intent.UpdatedAt = common.GetTimestamp()
	return nil
}

type CryptoChainScanCursor struct {
	Chain     string `json:"chain" gorm:"primaryKey;type:varchar(32)"`
	Cursor    string `json:"cursor" gorm:"type:varchar(255);not null;default:''"`
	UpdatedAt int64  `json:"updated_at" gorm:"bigint;not null;default:0"`
}

func normalizeCryptoAddress(chain, value string) string {
	value = strings.TrimSpace(value)
	if chain == "tron" || chain == "solana" {
		return value
	}
	return strings.ToLower(value)
}

func normalizeCryptoTxHash(chain, value string) string {
	value = strings.TrimSpace(value)
	if chain == "tron" || chain == "solana" {
		return value
	}
	return strings.ToLower(value)
}
