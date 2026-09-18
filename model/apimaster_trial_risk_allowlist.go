package model

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var trialRiskBlockedRemarkPattern = regexp.MustCompile(`\s*\[GPT_TRIAL_BLOCKED:[^\]]+\]`)
var trialRiskAllowlistSchemaMu sync.Mutex
var trialRiskAllowlistSchemaDB *gorm.DB

type TrialRiskAllowlist struct {
	ApimasterUserId string     `gorm:"column:apimaster_user_id;type:varchar(36);primaryKey"`
	NewapiUserId    int        `gorm:"column:newapi_user_id;not null;uniqueIndex"`
	Active          bool       `gorm:"column:active;not null;default:true;index"`
	CreatedById     int        `gorm:"column:created_by_id;not null"`
	CreatedBy       string     `gorm:"column:created_by;type:varchar(255);not null"`
	UpdatedById     int        `gorm:"column:updated_by_id;not null"`
	UpdatedBy       string     `gorm:"column:updated_by;type:varchar(255);not null"`
	CreatedAt       time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt       time.Time  `gorm:"column:updated_at;not null"`
	RevokedAt       *time.Time `gorm:"column:revoked_at"`
}

func (TrialRiskAllowlist) TableName() string {
	return "trial_risk_allowlist"
}

func ensureApimasterTrialRiskAllowlistSchema() error {
	if APIMASTER_PG_DB == nil {
		return errors.New("APIMASTER_PG_DSN 未配置")
	}
	trialRiskAllowlistSchemaMu.Lock()
	defer trialRiskAllowlistSchemaMu.Unlock()
	if trialRiskAllowlistSchemaDB == APIMASTER_PG_DB {
		return nil
	}
	if err := APIMASTER_PG_DB.AutoMigrate(&TrialRiskAllowlist{}); err != nil {
		return err
	}
	trialRiskAllowlistSchemaDB = APIMASTER_PG_DB
	return nil
}

// EnsureTrialRiskAllowlistSchema creates the account-level allowlist table
// during startup, before an administrator first opens the user list.
func EnsureTrialRiskAllowlistSchema() error {
	return ensureApimasterTrialRiskAllowlistSchema()
}

func apimasterUserIdSelectExpression() string {
	if APIMASTER_PG_DB != nil && APIMASTER_PG_DB.Dialector.Name() == "postgres" {
		return "id::text"
	}
	return "CAST(id AS TEXT)"
}

func apimasterDerivedUsernameExpression() string {
	if APIMASTER_PG_DB != nil && APIMASTER_PG_DB.Dialector.Name() == "postgres" {
		return "LEFT(REPLACE(id::text, '-', ''), 20)"
	}
	return "SUBSTR(REPLACE(CAST(id AS TEXT), '-', ''), 1, 20)"
}

func resolveApimasterUserIdForTrialAllowlist(user *User) (string, error) {
	if user == nil || user.Id <= 0 {
		return "", errors.New("用户无效")
	}

	type identityRow struct {
		ApimasterUserId string `gorm:"column:apimaster_user_id"`
	}
	var rows []identityRow
	if APIMASTER_PG_DB.Migrator().HasTable("trial_claims") {
		if err := APIMASTER_PG_DB.Raw(`
			SELECT CAST(apimaster_user_id AS TEXT) AS apimaster_user_id
			FROM trial_claims
			WHERE newapi_user_id = ?
			LIMIT 2
		`, user.Id).Scan(&rows).Error; err != nil {
			return "", err
		}
		if len(rows) == 1 && rows[0].ApimasterUserId != "" {
			return rows[0].ApimasterUserId, nil
		}
		if len(rows) > 1 {
			return "", errors.New("体验卡账号映射不唯一")
		}
	}

	rows = nil
	if strings.TrimSpace(user.Username) != "" {
		query := fmt.Sprintf(`
			SELECT %s AS apimaster_user_id
			FROM users
			WHERE %s = ?
			LIMIT 2
		`, apimasterUserIdSelectExpression(), apimasterDerivedUsernameExpression())
		if err := APIMASTER_PG_DB.Raw(query, user.Username).Scan(&rows).Error; err != nil {
			return "", err
		}
		if len(rows) == 1 && rows[0].ApimasterUserId != "" {
			return rows[0].ApimasterUserId, nil
		}
		if len(rows) > 1 {
			return "", errors.New("体验卡账号映射不唯一")
		}
	}

	rows = nil
	if strings.TrimSpace(user.Email) != "" {
		query := fmt.Sprintf(`
			SELECT %s AS apimaster_user_id
			FROM users
			WHERE LOWER(email) = LOWER(?)
			LIMIT 2
		`, apimasterUserIdSelectExpression())
		if err := APIMASTER_PG_DB.Raw(query, strings.TrimSpace(user.Email)).Scan(&rows).Error; err != nil {
			return "", err
		}
		if len(rows) == 1 && rows[0].ApimasterUserId != "" {
			return rows[0].ApimasterUserId, nil
		}
		if len(rows) > 1 {
			return "", errors.New("体验卡邮箱映射不唯一")
		}
	}

	return "", errors.New("未找到对应的 APIMaster 账号，无法设置体验卡白名单")
}

func SetTrialRiskAllowlist(user *User, active bool, adminId int, adminUsername string) (bool, error) {
	if err := ensureApimasterTrialRiskAllowlistSchema(); err != nil {
		return false, err
	}
	apimasterUserId, err := resolveApimasterUserIdForTrialAllowlist(user)
	if err != nil {
		return false, err
	}

	err = APIMASTER_PG_DB.Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		if active {
			row := TrialRiskAllowlist{
				ApimasterUserId: apimasterUserId,
				NewapiUserId:    user.Id,
				Active:          true,
				CreatedById:     adminId,
				CreatedBy:       adminUsername,
				UpdatedById:     adminId,
				UpdatedBy:       adminUsername,
				CreatedAt:       now,
				UpdatedAt:       now,
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "apimaster_user_id"}},
				DoUpdates: clause.Assignments(map[string]interface{}{
					"newapi_user_id": user.Id,
					"active":         true,
					"updated_by_id":  adminId,
					"updated_by":     adminUsername,
					"updated_at":     now,
					"revoked_at":     nil,
				}),
			}).Create(&row).Error; err != nil {
				return err
			}

			if tx.Migrator().HasTable("trial_claims") {
				updates := map[string]interface{}{
					"risk_status":     "allowed",
					"risk_reason":     nil,
					"risk_checked_at": now,
					"updated_at":      now,
				}
				if err := tx.Table("trial_claims").
					Where("CAST(apimaster_user_id AS TEXT) = ?", apimasterUserId).
					Where("claim_status NOT IN ?", []string{"granted", "claiming"}).
					Updates(updates).Error; err != nil {
					return err
				}
				if err := tx.Table("trial_claims").
					Where("CAST(apimaster_user_id AS TEXT) = ? AND claim_status = ?", apimasterUserId, "risk_blocked").
					Update("claim_status", "not_claimed").Error; err != nil {
					return err
				}
			}
			return nil
		}

		return tx.Model(&TrialRiskAllowlist{}).
			Where("apimaster_user_id = ?", apimasterUserId).
			Updates(map[string]interface{}{
				"active":        false,
				"updated_by_id": adminId,
				"updated_by":    adminUsername,
				"updated_at":    now,
				"revoked_at":    now,
			}).Error
	})
	if err != nil {
		return false, err
	}
	return active, nil
}

func ClearTrialRiskBlockedRemark(user *User) error {
	if user == nil || user.Id <= 0 {
		return errors.New("用户无效")
	}
	clean := strings.TrimSpace(trialRiskBlockedRemarkPattern.ReplaceAllString(user.Remark, ""))
	if clean == user.Remark {
		return nil
	}
	if err := DB.Model(&User{}).Where("id = ?", user.Id).Update("remark", clean).Error; err != nil {
		return err
	}
	user.Remark = clean
	return nil
}

func EnrichUsersTrialRiskAllowlist(users []*User) {
	if APIMASTER_PG_DB == nil || len(users) == 0 {
		return
	}
	if err := ensureApimasterTrialRiskAllowlistSchema(); err != nil {
		common.SysLog("failed to ensure trial risk allowlist schema: " + err.Error())
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

	var allowlistedIds []int
	if err := APIMASTER_PG_DB.Model(&TrialRiskAllowlist{}).
		Where("newapi_user_id IN ? AND active = ?", ids, true).
		Pluck("newapi_user_id", &allowlistedIds).Error; err != nil {
		common.SysLog("failed to enrich trial risk allowlist: " + err.Error())
		return
	}
	allowlisted := make(map[int]struct{}, len(allowlistedIds))
	for _, id := range allowlistedIds {
		allowlisted[id] = struct{}{}
	}
	for _, user := range users {
		if user == nil {
			continue
		}
		_, user.TrialRiskAllowlisted = allowlisted[user.Id]
	}
}
