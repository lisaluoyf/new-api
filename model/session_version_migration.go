package model

import (
	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

func migrateUserSessionVersion() error {
	if !common.UsingPostgreSQL {
		return nil
	}
	return withMigrationLimits(DB, func(tx *gorm.DB) error {
		return tx.Exec("ALTER TABLE users ADD COLUMN IF NOT EXISTS session_version BIGINT NOT NULL DEFAULT 0").Error
	})
}
