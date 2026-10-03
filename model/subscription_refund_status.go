package model

import (
	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// Repair the old action name stored as a status. Only status strings change:
// never replay refunds, adjust balances, or revoke entitlement a second time.
func normalizeLegacyRefundStatuses(db *gorm.DB) error {
	return withMigrationLimits(db, func(db *gorm.DB) error {
		return db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&SubscriptionOrder{}).Where("status = ?", "refund").
				Update("status", common.TopUpStatusRefunded).Error; err != nil {
				return err
			}
			return tx.Model(&TopUp{}).Where("status = ?", "refund").
				Update("status", common.TopUpStatusRefunded).Error
		})
	})
}
