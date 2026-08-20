package model

import (
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTelegramVerificationTestDB(t *testing.T) {
	t.Helper()
	oldDB := DB
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}, &TelegramGroupVerification{}))
	DB = db
	t.Cleanup(func() { DB = oldDB })
}

func createTelegramVerificationTestUser(t *testing.T, id int, username string) {
	t.Helper()
	require.NoError(t, DB.Create(&User{Id: id, Username: username, AffCode: fmt.Sprintf("tg-aff-%d", id)}).Error)
}

func TestTelegramVerificationTokenIsHashedAndExpires(t *testing.T) {
	setupTelegramVerificationTestDB(t)
	createTelegramVerificationTestUser(t, 1, "telegram-user-1")
	now := time.Date(2026, 8, 20, 4, 0, 0, 0, time.UTC)

	verification, token, err := StartTelegramGroupVerification(1, now)
	require.NoError(t, err)
	require.Len(t, token, 43)
	require.NotEqual(t, token, verification.TokenHash)
	require.Equal(t, HashTelegramVerificationToken(token), verification.TokenHash)
	require.Equal(t, now.Add(TelegramVerificationTTL), verification.TokenExpiresAt)

	_, _, err = ConsumeTelegramVerification(token, "10001", now.Add(TelegramVerificationTTL+time.Second))
	require.ErrorIs(t, err, ErrTelegramVerificationExpired)
}

func TestTelegramVerificationIsSingleUseAndIdempotentForSameAccount(t *testing.T) {
	setupTelegramVerificationTestDB(t)
	createTelegramVerificationTestUser(t, 1, "telegram-user-1")
	now := time.Date(2026, 8, 20, 4, 0, 0, 0, time.UTC)

	_, token, err := StartTelegramGroupVerification(1, now)
	require.NoError(t, err)
	verification, replay, err := ConsumeTelegramVerification(token, "10001", now.Add(time.Minute))
	require.NoError(t, err)
	require.False(t, replay)
	require.NotNil(t, verification.TelegramID)
	require.Equal(t, "10001", *verification.TelegramID)

	_, replay, err = ConsumeTelegramVerification(token, "10001", now.Add(TelegramVerificationTTL+time.Hour))
	require.NoError(t, err)
	require.True(t, replay)

	_, _, err = ConsumeTelegramVerification(token, "10002", now.Add(2*time.Minute))
	require.ErrorIs(t, err, ErrTelegramVerificationUsed)
}

func TestTelegramAccountCannotVerifyMultipleAPIMasterUsers(t *testing.T) {
	setupTelegramVerificationTestDB(t)
	createTelegramVerificationTestUser(t, 1, "telegram-user-1")
	createTelegramVerificationTestUser(t, 2, "telegram-user-2")
	now := time.Date(2026, 8, 20, 4, 0, 0, 0, time.UTC)

	_, firstToken, err := StartTelegramGroupVerification(1, now)
	require.NoError(t, err)
	_, _, err = ConsumeTelegramVerification(firstToken, "10001", now.Add(time.Minute))
	require.NoError(t, err)

	_, secondToken, err := StartTelegramGroupVerification(2, now)
	require.NoError(t, err)
	_, _, err = ConsumeTelegramVerification(secondToken, "10001", now.Add(time.Minute))
	require.ErrorIs(t, err, ErrTelegramAccountAlreadyLinked)

	var secondUser User
	require.NoError(t, DB.First(&secondUser, 2).Error)
	require.Empty(t, secondUser.TelegramId)
}
