package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupSelfDeleteDB(t *testing.T) (*gorm.DB, *gorm.DB, User) {
	t.Helper()
	oldDB, oldIdentity, oldRedis := DB, APIMASTER_PG_DB, common.RedisEnabled
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	identity, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}, &Token{}))
	require.NoError(t, identity.Exec("CREATE TABLE users (id TEXT PRIMARY KEY, status TEXT, email TEXT)").Error)
	require.NoError(t, identity.Exec("INSERT INTO users VALUES (?, 'active', ?)", "dcd605b1-6990-480c-a3f1-d2ca05219904", "original@example.test").Error)
	hash, err := common.Password2Hash("derived-password")
	require.NoError(t, err)
	user := User{Username: "dcd605b16990480ca3f1", Password: hash, Status: common.UserStatusEnabled, Role: common.RoleCommonUser, AffCode: "self-delete"}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&Token{UserId: user.Id, Key: "delete-test-key", Status: common.TokenStatusEnabled}).Error)
	DB, APIMASTER_PG_DB, common.RedisEnabled = db, identity, false
	t.Cleanup(func() { DB, APIMASTER_PG_DB, common.RedisEnabled = oldDB, oldIdentity, oldRedis })
	return db, identity, user
}

func TestDeleteSelfAccountClosesBothIdentitiesAndTokens(t *testing.T) {
	db, identity, user := setupSelfDeleteDB(t)
	// An editable email must not affect immutable account ownership.
	require.NoError(t, identity.Exec("INSERT INTO users VALUES ('other', 'active', 'changed@example.test')").Error)
	require.NoError(t, db.Model(&user).Update("email", "changed@example.test").Error)
	require.NoError(t, DeleteSelfAccount(&user))
	var current User
	require.NoError(t, db.Unscoped().First(&current, user.Id).Error)
	require.True(t, current.DeletedAt.Valid)
	require.Equal(t, common.UserStatusDisabled, current.Status)
	var status string
	require.NoError(t, identity.Table("users").Select("status").Where("id <> ?", "other").Scan(&status).Error)
	require.Equal(t, "deleted", status)
	require.NoError(t, identity.Table("users").Select("status").Where("id = ?", "other").Scan(&status).Error)
	require.Equal(t, "active", status)
	var token Token
	require.NoError(t, db.First(&token).Error)
	require.Equal(t, common.TokenStatusDisabled, token.Status)
	login := User{Username: user.Username, Password: "derived-password"}
	require.ErrorIs(t, login.ValidateAndFill(), ErrInvalidCredentials)
}

func TestDeleteSelfAccountRollsBackWhenIdentityWriteFails(t *testing.T) {
	db, identity, user := setupSelfDeleteDB(t)
	require.NoError(t, identity.Exec("CREATE TRIGGER reject_close BEFORE UPDATE ON users BEGIN SELECT RAISE(ABORT, 'identity unavailable'); END").Error)
	require.Error(t, DeleteSelfAccount(&user))
	var current User
	require.NoError(t, db.First(&current, user.Id).Error)
	require.Equal(t, common.UserStatusEnabled, current.Status)
	var token Token
	require.NoError(t, db.First(&token).Error)
	require.Equal(t, common.TokenStatusEnabled, token.Status)
}

func TestDeleteSelfAccountRejectsRootAndMissingIdentityDB(t *testing.T) {
	db, _, user := setupSelfDeleteDB(t)
	require.NoError(t, db.Model(&user).Update("role", common.RoleRootUser).Error)
	require.Error(t, DeleteSelfAccount(&user))
	require.NoError(t, db.Model(&user).Update("role", common.RoleCommonUser).Error)
	APIMASTER_PG_DB = nil
	t.Setenv("APIMASTER_PG_DSN", "configured-but-unavailable")
	require.Error(t, DeleteSelfAccount(&user))
	var current User
	require.NoError(t, db.First(&current, user.Id).Error)
}
