package model

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// Bound each schema operation, rather than keeping locks across all tables.
// PostgreSQL SET LOCAL is transaction-scoped and cannot leak into the request pool.
func withMigrationLimits(db *gorm.DB, migrate func(*gorm.DB) error) error {
	ctx, cancel := context.WithTimeout(db.Statement.Context, 60*time.Second)
	defer cancel()
	db = db.WithContext(ctx)
	if db.Dialector.Name() != "postgres" {
		return migrate(db)
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL lock_timeout = '3s'").Error; err != nil {
			return err
		}
		if err := tx.Exec("SET LOCAL statement_timeout = '60s'").Error; err != nil {
			return err
		}
		return migrate(tx)
	})
}

func autoMigrateWithLimits(db *gorm.DB, models ...interface{}) error {
	for _, model := range models {
		if err := withMigrationLimits(db, func(tx *gorm.DB) error {
			return tx.AutoMigrate(model)
		}); err != nil {
			return err
		}
	}
	return nil
}
