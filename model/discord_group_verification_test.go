package model

import (
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupDiscordVerificationTestDB(t *testing.T) {
	t.Helper()
	oldDB := DB
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&DiscordGroupVerification{}))
	DB = db
	t.Cleanup(func() { DB = oldDB })
}

func TestDiscordGroupVerificationTracksLeaveAndRejoin(t *testing.T) {
	setupDiscordVerificationTestDB(t)
	joinedAt := time.Date(2026, 8, 25, 2, 0, 0, 0, time.UTC)
	require.NoError(t, RecordDiscordGroupVerification(1, "guild-1", true, joinedAt))

	verification, err := GetDiscordGroupVerification(1, "guild-1")
	require.NoError(t, err)
	require.NotNil(t, verification.JoinedAt)
	require.WithinDuration(t, joinedAt, *verification.JoinedAt, time.Second)

	checkedAgainAt := joinedAt.Add(time.Minute)
	require.NoError(t, RecordDiscordGroupVerification(1, "guild-1", true, checkedAgainAt))
	verification, err = GetDiscordGroupVerification(1, "guild-1")
	require.NoError(t, err)
	require.WithinDuration(t, joinedAt, *verification.JoinedAt, time.Second)
	require.WithinDuration(t, checkedAgainAt, verification.CheckedAt, time.Second)

	leftAt := joinedAt.Add(2 * time.Minute)
	require.NoError(t, RecordDiscordGroupVerification(1, "guild-1", false, leftAt))
	verification, err = GetDiscordGroupVerification(1, "guild-1")
	require.NoError(t, err)
	require.Nil(t, verification.JoinedAt)

	rejoinedAt := joinedAt.Add(3 * time.Minute)
	require.NoError(t, RecordDiscordGroupVerification(1, "guild-1", true, rejoinedAt))
	verification, err = GetDiscordGroupVerification(1, "guild-1")
	require.NoError(t, err)
	require.NotNil(t, verification.JoinedAt)
	require.WithinDuration(t, rejoinedAt, *verification.JoinedAt, time.Second)
}

func TestDiscordGroupVerificationSeparatesGuilds(t *testing.T) {
	setupDiscordVerificationTestDB(t)
	now := time.Date(2026, 8, 25, 2, 0, 0, 0, time.UTC)
	require.NoError(t, RecordDiscordGroupVerification(1, "guild-1", true, now))
	require.NoError(t, RecordDiscordGroupVerification(1, "guild-2", false, now))

	first, err := GetDiscordGroupVerification(1, "guild-1")
	require.NoError(t, err)
	require.NotNil(t, first.JoinedAt)
	second, err := GetDiscordGroupVerification(1, "guild-2")
	require.NoError(t, err)
	require.Nil(t, second.JoinedAt)
}
