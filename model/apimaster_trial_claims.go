package model

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
)

func protectedTrialClaimPredicate(tableAlias string) string {
	prefix := ""
	if tableAlias != "" {
		prefix = tableAlias + "."
	}
	if APIMASTER_PG_DB != nil && APIMASTER_PG_DB.Dialector.Name() == "sqlite" {
		return fmt.Sprintf("(%[1]sclaim_status = 'granted' OR (%[1]sclaim_status = 'claiming' AND %[1]sclaim_started_at >= datetime('now', '-5 minutes')))", prefix)
	}
	return fmt.Sprintf("(%[1]sclaim_status = 'granted' OR (%[1]sclaim_status = 'claiming' AND %[1]sclaim_started_at >= now() - interval '5 minutes'))", prefix)
}

// ReleaseUnclaimedTelegramTrialReservation releases a Telegram identity that
// was checked but never used to issue a GPT Trial. Once a claim is granted, the
// reservation must remain permanent and this function is not called.
func ReleaseUnclaimedTelegramTrialReservation(userID int) error {
	if APIMASTER_PG_DB == nil {
		return nil
	}
	return APIMASTER_PG_DB.Exec(fmt.Sprintf(`
		DELETE FROM trial_social_identities
		WHERE provider = 'telegram'
		  AND provider_user_id = ?
		  AND NOT EXISTS (
			SELECT 1
			FROM trial_claims
			WHERE trial_claims.apimaster_user_id = trial_social_identities.apimaster_user_id
			  AND %s
		  )
	`, protectedTrialClaimPredicate("trial_claims")), "newapi:"+fmt.Sprint(userID)).Error
}

// HasGrantedTrialClaim reports whether the New API user has already received,
// or is currently receiving, the APIMaster GPT Trial. In-flight claims must be
// protected from unlink/release races just like granted claims. If
// APIMASTER_PG_DSN is configured, query failures are returned so callers do not
// accidentally release a used social identity.
func HasGrantedTrialClaim(userID int) (bool, error) {
	if APIMASTER_PG_DB == nil {
		return false, nil
	}
	var count int64
	err := APIMASTER_PG_DB.Raw(fmt.Sprintf(`
		SELECT COUNT(*)
		FROM trial_claims
		JOIN trial_social_identities
		  ON trial_social_identities.apimaster_user_id = trial_claims.apimaster_user_id
		WHERE trial_social_identities.provider = 'telegram'
		  AND trial_social_identities.provider_user_id = ?
		  AND %s
	`, protectedTrialClaimPredicate("trial_claims")), "newapi:"+fmt.Sprint(userID)).Scan(&count).Error
	return count > 0, err
}

// EnrichUsersTrialClaimStatus batch-loads each user's GPT trial claim_status
// from apimaster's trial_claims table (keyed directly by newapi_user_id,
// unlike EnrichUsersRegistrationChannels which has to derive the join key from
// the apimaster UUID since trial_claims already stores the new-api integer id).
func EnrichUsersTrialClaimStatus(users []*User) {
	if APIMASTER_PG_DB == nil || len(users) == 0 {
		return
	}
	ids := make([]int, 0, len(users))
	for _, user := range users {
		if user != nil {
			ids = append(ids, user.Id)
		}
	}
	if len(ids) == 0 {
		return
	}

	type claimRow struct {
		NewapiUserId int
		ClaimStatus  string
	}
	var rows []claimRow
	err := APIMASTER_PG_DB.Raw(`
		SELECT newapi_user_id, claim_status
		FROM trial_claims
		WHERE newapi_user_id IN ?
	`, ids).Scan(&rows).Error
	if err != nil {
		common.SysLog("failed to enrich user trial claim status: " + err.Error())
		return
	}

	statusById := make(map[int]string, len(rows))
	for _, row := range rows {
		statusById[row.NewapiUserId] = row.ClaimStatus
	}
	for _, user := range users {
		if user == nil {
			continue
		}
		if status, ok := statusById[user.Id]; ok {
			user.TrialClaimStatus = status
		}
	}
}

// FindUserIdsByTrialStatus returns the new-api user ids whose trial claim_status
// matches the given value, for use as a WHERE id IN (...) filter against the
// main users table. "not_claimed" also matches users with no trial_claims row
// at all, so it is handled by the caller via NOT IN instead of calling this.
func FindUserIdsByTrialStatus(status string) ([]int, error) {
	if APIMASTER_PG_DB == nil {
		return nil, nil
	}
	var ids []int
	err := APIMASTER_PG_DB.Raw(`
		SELECT newapi_user_id FROM trial_claims WHERE claim_status = ?
	`, status).Scan(&ids).Error
	if err != nil {
		return nil, err
	}
	return ids, nil
}

// FindUserIdsWithAnyTrialClaim returns every new-api user id that has a row in
// trial_claims, regardless of status — used to compute the "not_claimed"
// filter as the complement set (main users table minus this set).
func FindUserIdsWithAnyTrialClaim() ([]int, error) {
	if APIMASTER_PG_DB == nil {
		return nil, nil
	}
	var ids []int
	err := APIMASTER_PG_DB.Raw(`SELECT newapi_user_id FROM trial_claims`).Scan(&ids).Error
	if err != nil {
		return nil, err
	}
	return ids, nil
}
