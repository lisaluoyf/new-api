package model

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DeleteSelfAccount closes the identity and gateway accounts. Keep a tombstone
// and billing history; login must never turn an explicit deletion into repair.
func DeleteSelfAccount(user *User) error {
	if user == nil || user.Id <= 0 || user.Role == common.RoleRootUser {
		return errors.New("invalid account for self deletion")
	}
	if APIMASTER_PG_DB == nil && strings.TrimSpace(os.Getenv("APIMASTER_PG_DSN")) != "" {
		return errors.New("identity database unavailable")
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		var current User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, user.Id).Error; err != nil {
			return err
		}
		if current.Role == common.RoleRootUser {
			return errors.New("cannot delete root account")
		}
		if err := tx.Model(&current).Updates(map[string]interface{}{"status": common.UserStatusDisabled, "access_token": nil}).Error; err != nil {
			return err
		}
		if err := tx.Model(&Token{}).Where("user_id = ?", current.Id).Update("status", common.TokenStatusDisabled).Error; err != nil {
			return err
		}
		if err := tx.Delete(&current).Error; err != nil {
			return err
		}
		if APIMASTER_PG_DB != nil {
			// Resolve only by immutable UUID-derived username, never by editable
			// email. An email match alone is not authority to close an identity.
			var identities []struct{ ID string }
			if err := APIMASTER_PG_DB.Table("users").Select(apimasterUserIdSelectExpression()+" AS id").
				Where(apimasterDerivedUsernameExpression()+" = ?", current.Username).Limit(2).Scan(&identities).Error; err != nil {
				return err
			}
			if len(identities) != 1 {
				return errors.New("missing or ambiguous identity mapping")
			}
			if len(identities) == 1 {
				// Commit the identity tombstone before the gateway transaction.
				// If the final gateway commit fails, leave identity login closed;
				// never compensate by reactivating an intentionally deleted user.
				result := APIMASTER_PG_DB.Table("users").Where("id = ?", identities[0].ID).Update("status", "deleted")
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return errors.New("identity disappeared during deletion")
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	userErr := invalidateUserCache(user.Id)
	tokenErr := InvalidateUserTokensCache(user.Id)
	if userErr != nil || tokenErr != nil {
		return fmt.Errorf("account closed but cache cleanup failed: user=%v token=%v", userErr, tokenErr)
	}
	return nil
}
