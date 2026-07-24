package model

import "github.com/QuantumNous/new-api/common"

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
