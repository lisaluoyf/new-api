package model

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const TelegramVerificationTTL = 10 * time.Minute

var (
	ErrTelegramVerificationNotFound = errors.New("telegram verification not found")
	ErrTelegramVerificationExpired  = errors.New("telegram verification expired")
	ErrTelegramVerificationUsed     = errors.New("telegram verification already used")
	ErrTelegramAccountAlreadyLinked = errors.New("telegram account already linked")
	ErrTelegramVerificationUserGone = errors.New("telegram verification user not found")
)

// TelegramGroupVerification maps an APIMaster user to the Telegram identity
// needed by getChatMember. Raw deep-link tokens are never persisted.
type TelegramGroupVerification struct {
	ID              int        `json:"id" gorm:"primaryKey"`
	UserID          int        `json:"user_id" gorm:"column:user_id;not null;uniqueIndex"`
	TokenHash       string     `json:"-" gorm:"column:token_hash;type:char(64);not null;uniqueIndex"`
	TelegramID      *string    `json:"-" gorm:"column:telegram_id;type:varchar(32);uniqueIndex"`
	TokenExpiresAt  time.Time  `json:"token_expires_at" gorm:"column:token_expires_at;not null;index"`
	TokenConsumedAt *time.Time `json:"token_consumed_at" gorm:"column:token_consumed_at"`
	IdentifiedAt    *time.Time `json:"identified_at" gorm:"column:identified_at"`
	VerifiedAt      *time.Time `json:"verified_at" gorm:"column:verified_at"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func (TelegramGroupVerification) TableName() string {
	return "telegram_group_verifications"
}

func NewTelegramGroupVerification(userID int, now time.Time) (*TelegramGroupVerification, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	return &TelegramGroupVerification{
		UserID:         userID,
		TokenHash:      HashTelegramVerificationToken(token),
		TokenExpiresAt: now.UTC().Add(TelegramVerificationTTL),
	}, token, nil
}

func HashTelegramVerificationToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func GetTelegramGroupVerificationByUserID(userID int) (*TelegramGroupVerification, error) {
	var verification TelegramGroupVerification
	err := DB.Where("user_id = ?", userID).First(&verification).Error
	return &verification, err
}

// StartTelegramGroupVerification rotates the pending deep-link token. Once a
// Telegram identity is known, callers should send the user straight to the
// community instead of asking them to identify again.
func StartTelegramGroupVerification(userID int, now time.Time) (*TelegramGroupVerification, string, error) {
	var existing TelegramGroupVerification
	err := DB.Where("user_id = ?", userID).First(&existing).Error
	if err == nil && existing.TelegramID != nil && *existing.TelegramID != "" {
		return &existing, "", nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, "", err
	}

	verification, token, err := NewTelegramGroupVerification(userID, now)
	if err != nil {
		return nil, "", err
	}
	updates := map[string]interface{}{
		"token_hash":        verification.TokenHash,
		"token_expires_at":  verification.TokenExpiresAt,
		"token_consumed_at": nil,
	}
	if err := DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.Assignments(updates),
	}).Create(verification).Error; err != nil {
		return nil, "", err
	}
	if err := DB.Where("user_id = ? AND token_hash = ?", userID, verification.TokenHash).First(verification).Error; err != nil {
		return nil, "", err
	}
	return verification, token, nil
}

// ConsumeTelegramVerification identifies the APIMaster user from a private
// Bot /start message. Re-delivery of the same Telegram update is idempotent;
// using the token from another Telegram account is rejected.
func ConsumeTelegramVerification(token, telegramID string, now time.Time) (*TelegramGroupVerification, bool, error) {
	var result TelegramGroupVerification
	var replay bool
	err := DB.Transaction(func(tx *gorm.DB) error {
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("token_hash = ?", HashTelegramVerificationToken(token)).
			First(&result)
		if errors.Is(query.Error, gorm.ErrRecordNotFound) {
			return ErrTelegramVerificationNotFound
		}
		if query.Error != nil {
			return query.Error
		}
		if result.TokenConsumedAt != nil {
			if result.TelegramID != nil && *result.TelegramID == telegramID {
				replay = true
				return nil
			}
			return ErrTelegramVerificationUsed
		}
		if now.UTC().After(result.TokenExpiresAt.UTC()) {
			return ErrTelegramVerificationExpired
		}

		var user User
		if err := tx.Select("id, telegram_id").Where("id = ?", result.UserID).First(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrTelegramVerificationUserGone
			}
			return err
		}
		var count int64
		if err := tx.Model(&TelegramGroupVerification{}).
			Where("telegram_id = ? AND user_id <> ?", telegramID, result.UserID).
			Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return ErrTelegramAccountAlreadyLinked
		}
		if err := tx.Unscoped().Model(&User{}).
			Where("telegram_id = ? AND id <> ?", telegramID, result.UserID).
			Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return ErrTelegramAccountAlreadyLinked
		}

		identifiedAt := now.UTC()
		result.TelegramID = &telegramID
		result.TokenConsumedAt = &identifiedAt
		result.IdentifiedAt = &identifiedAt
		if err := tx.Model(&result).Updates(map[string]interface{}{
			"telegram_id":       telegramID,
			"token_consumed_at": identifiedAt,
			"identified_at":     identifiedAt,
		}).Error; err != nil {
			return err
		}
		return tx.Model(&User{}).Where("id = ?", result.UserID).
			Update("telegram_id", telegramID).Error
	})
	return &result, replay, err
}

func MarkTelegramGroupVerified(userID int, joined bool, now time.Time) error {
	updates := map[string]interface{}{"verified_at": nil}
	if joined {
		updates["verified_at"] = now.UTC()
	}
	return DB.Model(&TelegramGroupVerification{}).
		Where("user_id = ?", userID).
		Updates(updates).Error
}
