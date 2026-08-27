package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupInternalRestoreTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	oldDB := DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&User{}))
	DB = db
	t.Cleanup(func() { DB = oldDB })
	return db
}

func createDeletedRestoreUser(t *testing.T, db *gorm.DB, username, password string, status int) User {
	t.Helper()
	hash, err := common.Password2Hash(password)
	require.NoError(t, err)
	user := User{
		Username: username,
		Password: hash,
		Status:   status,
		AffCode:  username,
	}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Delete(&user).Error)
	return user
}

func TestValidateDeletedMirrorAndRestore(t *testing.T) {
	db := setupInternalRestoreTestDB(t)
	created := createDeletedRestoreUser(t, db, "d5b64e87616c4001bbfc", "derived-password", common.UserStatusEnabled)

	login := User{Username: created.Username, Password: "derived-password"}
	restored, err := login.ValidateDeletedMirrorAndRestore()
	require.NoError(t, err)
	require.True(t, restored)
	require.Equal(t, created.Id, login.Id)
	require.False(t, login.DeletedAt.Valid)

	var persisted User
	require.NoError(t, db.First(&persisted, created.Id).Error)
	require.False(t, persisted.DeletedAt.Valid)
}

func TestValidateDeletedMirrorAndRestoreRejectsUnsafeCandidates(t *testing.T) {
	tests := []struct {
		name     string
		username string
		password string
		status   int
		attempt  string
	}{
		{name: "wrong password", username: "11111111111111111111", password: "right-password", status: common.UserStatusEnabled, attempt: "wrong-password"},
		{name: "non derived username", username: "ordinary-user", password: "right-password", status: common.UserStatusEnabled, attempt: "right-password"},
		{name: "disabled account", username: "22222222222222222222", password: "right-password", status: common.UserStatusDisabled, attempt: "right-password"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupInternalRestoreTestDB(t)
			created := createDeletedRestoreUser(t, db, test.username, test.password, test.status)
			login := User{Username: test.username, Password: test.attempt}
			restored, err := login.ValidateDeletedMirrorAndRestore()
			require.NoError(t, err)
			require.False(t, restored)

			var persisted User
			require.NoError(t, db.Unscoped().First(&persisted, created.Id).Error)
			require.True(t, persisted.DeletedAt.Valid)
		})
	}
}
