package model

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTrialRiskAllowlistTestDB(t *testing.T) {
	t.Helper()
	oldDB := DB
	oldApimasterDB := APIMASTER_PG_DB

	localDB, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s-local?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, localDB.AutoMigrate(&User{}))
	DB = localDB

	apimasterDB, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s-apimaster?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, apimasterDB.Exec(`
		CREATE TABLE users (
			id TEXT PRIMARY KEY,
			email TEXT NOT NULL
		)
	`).Error)
	require.NoError(t, apimasterDB.Exec(`
		CREATE TABLE trial_claims (
			apimaster_user_id TEXT PRIMARY KEY,
			newapi_user_id INTEGER,
			claim_status TEXT NOT NULL,
			risk_status TEXT,
			risk_reason TEXT,
			risk_checked_at DATETIME,
			updated_at DATETIME
		)
	`).Error)
	APIMASTER_PG_DB = apimasterDB

	t.Cleanup(func() {
		DB = oldDB
		APIMASTER_PG_DB = oldApimasterDB
	})
}

func TestSetTrialRiskAllowlistClearsExistingRiskWithoutGrantingTrial(t *testing.T) {
	setupTrialRiskAllowlistTestDB(t)
	uuid := "12345678-1234-1234-1234-123456789abc"
	user := &User{
		Id:       42,
		Username: "12345678123412341234",
		Email:    "allow@example.com",
		AffCode:  "allow042",
		Remark:   "manual note [GPT_TRIAL_BLOCKED:Region_blocked]",
	}
	require.NoError(t, DB.Create(user).Error)
	require.NoError(t, APIMASTER_PG_DB.Exec("INSERT INTO users (id, email) VALUES (?, ?)", uuid, user.Email).Error)
	require.NoError(t, APIMASTER_PG_DB.Exec(`
		INSERT INTO trial_claims (
			apimaster_user_id, newapi_user_id, claim_status, risk_status, risk_reason
		) VALUES (?, ?, 'risk_blocked', 'blocked', 'Region_blocked')
	`, uuid, user.Id).Error)

	active, err := SetTrialRiskAllowlist(user, true, 1, "root")
	require.NoError(t, err)
	require.True(t, active)
	require.NoError(t, ClearTrialRiskBlockedRemark(user))

	var allowlist TrialRiskAllowlist
	require.NoError(t, APIMASTER_PG_DB.First(&allowlist, "apimaster_user_id = ?", uuid).Error)
	require.True(t, allowlist.Active)
	require.Equal(t, user.Id, allowlist.NewapiUserId)
	require.Equal(t, 1, allowlist.CreatedById)
	require.Equal(t, "root", allowlist.CreatedBy)

	type claimState struct {
		ClaimStatus string
		RiskStatus  string
		RiskReason  *string
	}
	var claim claimState
	require.NoError(t, APIMASTER_PG_DB.Raw(`
		SELECT claim_status, risk_status, risk_reason
		FROM trial_claims
		WHERE apimaster_user_id = ?
	`, uuid).Scan(&claim).Error)
	require.Equal(t, "not_claimed", claim.ClaimStatus)
	require.Equal(t, "allowed", claim.RiskStatus)
	require.Nil(t, claim.RiskReason)

	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	require.Equal(t, "manual note", stored.Remark)
	require.Empty(t, stored.TrialClaimStatus)
}

func TestSetTrialRiskAllowlistIsIdempotentAndCanBeRevoked(t *testing.T) {
	setupTrialRiskAllowlistTestDB(t)
	uuid := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	user := &User{Id: 55, Username: "aaaaaaaabbbbccccdddd", Email: "repeat@example.com", AffCode: "allow055"}
	require.NoError(t, DB.Create(user).Error)
	require.NoError(t, APIMASTER_PG_DB.Exec("INSERT INTO users (id, email) VALUES (?, ?)", uuid, user.Email).Error)

	_, err := SetTrialRiskAllowlist(user, true, 1, "first-admin")
	require.NoError(t, err)
	_, err = SetTrialRiskAllowlist(user, true, 2, "second-admin")
	require.NoError(t, err)

	var count int64
	require.NoError(t, APIMASTER_PG_DB.Model(&TrialRiskAllowlist{}).Count(&count).Error)
	require.EqualValues(t, 1, count)

	active, err := SetTrialRiskAllowlist(user, false, 2, "second-admin")
	require.NoError(t, err)
	require.False(t, active)
	active, err = SetTrialRiskAllowlist(user, false, 2, "second-admin")
	require.NoError(t, err)
	require.False(t, active)

	var allowlist TrialRiskAllowlist
	require.NoError(t, APIMASTER_PG_DB.First(&allowlist, "apimaster_user_id = ?", uuid).Error)
	require.False(t, allowlist.Active)
	require.NotNil(t, allowlist.RevokedAt)
	require.Equal(t, 2, allowlist.UpdatedById)
}

func TestEnrichUsersTrialRiskAllowlist(t *testing.T) {
	setupTrialRiskAllowlistTestDB(t)
	first := &User{Id: 71}
	second := &User{Id: 72}
	require.NoError(t, APIMASTER_PG_DB.AutoMigrate(&TrialRiskAllowlist{}))
	require.NoError(t, APIMASTER_PG_DB.Create(&TrialRiskAllowlist{
		ApimasterUserId: "bbbbbbbb-1111-2222-3333-444444444444",
		NewapiUserId:    first.Id,
		Active:          true,
		CreatedById:     1,
		CreatedBy:       "root",
		UpdatedById:     1,
		UpdatedBy:       "root",
	}).Error)

	EnrichUsersTrialRiskAllowlist([]*User{first, second})
	require.True(t, first.TrialRiskAllowlisted)
	require.False(t, second.TrialRiskAllowlisted)
}

func TestSetTrialRiskAllowlistRejectsUnmappedAccount(t *testing.T) {
	setupTrialRiskAllowlistTestDB(t)
	user := &User{Id: 99, Username: "not-mapped", Email: "missing@example.com"}

	_, err := SetTrialRiskAllowlist(user, true, 1, "root")
	require.ErrorContains(t, err, "未找到对应的 APIMaster 账号")

	var count int64
	require.NoError(t, APIMASTER_PG_DB.Model(&TrialRiskAllowlist{}).Count(&count).Error)
	require.Zero(t, count)
}
