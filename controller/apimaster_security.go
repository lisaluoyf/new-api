package controller

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// InternalAccountSecurity is protected by RequireApimasterInternalSync. It
// never creates a login session or exposes the TOTP secret or backup hashes.
func InternalAccountSecurity(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
		Action   string `json:"action"`
		Code     string `json:"code"`
	}
	if c.ShouldBindJSON(&req) != nil || strings.TrimSpace(req.Username) == "" || (req.Action != "status" && req.Action != "verify") {
		c.JSON(400, gin.H{"success": false, "code": "INVALID_REQUEST"})
		return
	}
	var user model.User
	err := model.DB.Where("username = ?", req.Username).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) && req.Action == "status" {
		// A new website account may not have a console mirror yet.
		c.JSON(200, gin.H{"success": true, "data": gin.H{"enabled": false, "factor_id": 0}})
		return
	}
	if err != nil {
		c.JSON(503, gin.H{"success": false, "code": "ACCOUNT_UNAVAILABLE"})
		return
	}
	if user.Status != common.UserStatusEnabled {
		c.JSON(403, gin.H{"success": false, "code": "ACCOUNT_DISABLED"})
		return
	}
	var factor model.TwoFA
	verified, locked := false, false
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		q := tx.Where("user_id = ?", user.Id)
		if req.Action == "verify" {
			q = q.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		if err := q.First(&factor).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if req.Action != "verify" || !factor.IsEnabled {
			return nil
		}
		locked = factor.IsLocked()
		if locked {
			return nil
		}
		code, numericErr := common.ValidateNumericCode(req.Code)
		var verifiedStep *int64
		if numericErr == nil {
			step := time.Now().Unix() / 30
			for _, candidate := range []int64{step, step - 1, step + 1} {
				valid, err := totp.ValidateCustom(code, factor.Secret, time.Unix(candidate*30, 0), totp.ValidateOpts{Period: 30, Skew: 0, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
				if err != nil {
					return err
				}
				if valid && (factor.LastVerifiedStep == nil || candidate > *factor.LastVerifiedStep) {
					verified = true
					verifiedStep = &candidate
					break
				}
			}
		} else if common.ValidateBackupCode(req.Code) {
			var backups []model.TwoFABackupCode
			if err := tx.Where("user_id = ? AND is_used = ?", user.Id, false).Find(&backups).Error; err != nil {
				return err
			}
			for _, backup := range backups {
				if common.ValidatePasswordAndHash(common.NormalizeBackupCode(req.Code), backup.CodeHash) {
					r := tx.Model(&model.TwoFABackupCode{}).Where("id = ? AND is_used = ?", backup.Id, false).Updates(map[string]any{"is_used": true, "used_at": time.Now()})
					if r.Error != nil {
						return r.Error
					}
					verified = r.RowsAffected == 1
					break
				}
			}
		}
		if verified {
			updates := map[string]any{"failed_attempts": 0, "locked_until": nil, "last_used_at": time.Now()}
			if verifiedStep != nil {
				updates["last_verified_step"] = *verifiedStep
			}
			return tx.Model(&factor).Updates(updates).Error
		}
		attempts := factor.FailedAttempts + 1
		updates := map[string]any{"failed_attempts": attempts}
		if attempts >= common.MaxFailAttempts {
			updates["locked_until"] = time.Now().Add(time.Duration(common.LockoutDuration) * time.Second)
		}
		return tx.Model(&factor).Updates(updates).Error
	})
	if err != nil {
		c.JSON(503, gin.H{"success": false, "code": "ACCOUNT_UNAVAILABLE"})
		return
	}
	if req.Action == "verify" && (!factor.IsEnabled || !verified) {
		status, code := http.StatusUnauthorized, "TWO_FACTOR_INVALID"
		if locked {
			status, code = http.StatusTooManyRequests, "RATE_LIMITED"
		}
		c.JSON(status, gin.H{"success": false, "code": code})
		return
	}
	c.JSON(200, gin.H{"success": true, "data": gin.H{"enabled": factor.IsEnabled, "factor_id": factor.Id, "verified": verified}})
}
