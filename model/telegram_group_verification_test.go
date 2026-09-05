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

func setupApimasterTrialTestDB(t *testing.T) {
	t.Helper()
	oldDB := APIMASTER_PG_DB
	dsn := fmt.Sprintf("file:%s-apimaster?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`
		CREATE TABLE trial_social_identities (
			apimaster_user_id TEXT NOT NULL,
			provider TEXT NOT NULL,
			provider_user_id TEXT NOT NULL
		)
	`).Error)
	require.NoError(t, db.Exec(`
		CREATE TABLE trial_claims (
			apimaster_user_id TEXT NOT NULL,
			newapi_user_id INTEGER,
			claim_status TEXT NOT NULL,
			claim_started_at DATETIME
		)
	`).Error)
	require.NoError(t, db.Exec(`
		CREATE TABLE user_social_bindings (
			apimaster_user_id TEXT NOT NULL,
			provider TEXT NOT NULL,
			provider_user_id TEXT NOT NULL
		)
	`).Error)
	APIMASTER_PG_DB = db
	t.Cleanup(func() { APIMASTER_PG_DB = oldDB })
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

func TestClearTelegramGroupVerificationRemovesLiveBinding(t *testing.T) {
	setupTelegramVerificationTestDB(t)
	setupApimasterTrialTestDB(t)
	const username = "0123456789abcdef0123"
	createTelegramVerificationTestUser(t, 1, username)
	require.NoError(t, APIMASTER_PG_DB.Exec(`
		INSERT INTO user_social_bindings (apimaster_user_id, provider, provider_user_id)
		VALUES ('01234567-89ab-cdef-0123-456789abcdef', 'telegram', '10001')
	`).Error)
	now := time.Date(2026, 8, 20, 4, 0, 0, 0, time.UTC)

	_, token, err := StartTelegramGroupVerification(1, now)
	require.NoError(t, err)
	_, _, err = ConsumeTelegramVerification(token, "10001", now.Add(time.Minute))
	require.NoError(t, err)

	require.NoError(t, ClearTelegramGroupVerification(1, true))
	var user User
	require.NoError(t, DB.First(&user, 1).Error)
	require.Empty(t, user.TelegramId)
	_, err = GetTelegramGroupVerificationByUserID(1)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	var loginBindingCount int64
	require.NoError(t, APIMASTER_PG_DB.Raw(`
		SELECT COUNT(*) FROM user_social_bindings
		WHERE provider = 'telegram' AND provider_user_id = '10001'
	`).Scan(&loginBindingCount).Error)
	require.Zero(t, loginBindingCount)
}

func TestReleaseUnclaimedTelegramTrialReservationPreservesGrantedClaim(t *testing.T) {
	setupApimasterTrialTestDB(t)
	require.NoError(t, APIMASTER_PG_DB.Exec(`
		INSERT INTO trial_social_identities (apimaster_user_id, provider, provider_user_id)
		VALUES ('user-1', 'telegram', 'newapi:1'), ('user-2', 'telegram', 'newapi:2'),
		       ('user-3', 'telegram', 'newapi:3'), ('user-4', 'telegram', 'newapi:4')
	`).Error)
	require.NoError(t, APIMASTER_PG_DB.Exec(`
		INSERT INTO trial_claims (apimaster_user_id, newapi_user_id, claim_status, claim_started_at)
		VALUES ('user-2', 2, 'granted', NULL),
		       ('user-3', NULL, 'claiming', CURRENT_TIMESTAMP),
		       ('user-4', NULL, 'claiming', datetime('now', '-10 minutes'))
	`).Error)

	claimed, err := HasGrantedTrialClaim(1)
	require.NoError(t, err)
	require.False(t, claimed)
	require.NoError(t, ReleaseUnclaimedTelegramTrialReservation(1))

	claimed, err = HasGrantedTrialClaim(2)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, ReleaseUnclaimedTelegramTrialReservation(2))
	claimed, err = HasGrantedTrialClaim(3)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, ReleaseUnclaimedTelegramTrialReservation(3))
	claimed, err = HasGrantedTrialClaim(4)
	require.NoError(t, err)
	require.False(t, claimed)
	require.NoError(t, ReleaseUnclaimedTelegramTrialReservation(4))

	var count int64
	require.NoError(t, APIMASTER_PG_DB.Raw(`
		SELECT COUNT(*) FROM trial_social_identities
		WHERE provider = 'telegram' AND provider_user_id = 'newapi:1'
	`).Scan(&count).Error)
	require.Zero(t, count)
	require.NoError(t, APIMASTER_PG_DB.Raw(`
		SELECT COUNT(*) FROM trial_social_identities
		WHERE provider = 'telegram' AND provider_user_id = 'newapi:2'
	`).Scan(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, APIMASTER_PG_DB.Raw(`
		SELECT COUNT(*) FROM trial_social_identities
		WHERE provider = 'telegram' AND provider_user_id = 'newapi:3'
	`).Scan(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, APIMASTER_PG_DB.Raw(`
		SELECT COUNT(*) FROM trial_social_identities
		WHERE provider = 'telegram' AND provider_user_id = 'newapi:4'
	`).Scan(&count).Error)
	require.Zero(t, count)
}
