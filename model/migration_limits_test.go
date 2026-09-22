package model

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Run against an isolated development database, never a production DSN.
func TestMigrationLimitsPostgres(t *testing.T) {
	dsn := os.Getenv("APIMASTER_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("APIMASTER_TEST_POSTGRES_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Close() })
	table := fmt.Sprintf("migration_limit_test_%d", time.Now().UnixNano())
	require.NoError(t, db.Exec("CREATE TABLE "+table+" (id integer, label text)").Error)
	t.Cleanup(func() { db.Exec("DROP TABLE IF EXISTS " + table) })
	reader := db.Begin()
	require.NoError(t, reader.Error)
	defer reader.Rollback()
	var count int64
	require.NoError(t, reader.Table(table).Count(&count).Error)
	start := time.Now()
	err = withMigrationLimits(db, func(tx *gorm.DB) error {
		return tx.Exec("ALTER TABLE " + table + " ALTER COLUMN label SET DEFAULT ''").Error
	})
	require.ErrorContains(t, err, "lock timeout")
	require.Less(t, time.Since(start), 8*time.Second)
	// The waiting DDL must not survive the failed migration and block inserts.
	require.NoError(t, db.Exec("INSERT INTO "+table+" (id) VALUES (1)").Error)
	require.NoError(t, reader.Rollback().Error)

	err = withMigrationLimits(db, func(tx *gorm.DB) error {
		if err := tx.Exec("ALTER TABLE " + table + " ADD COLUMN aborted integer").Error; err != nil {
			return err
		}
		return errors.New("simulated migration failure")
	})
	require.ErrorContains(t, err, "simulated migration failure")
	require.False(t, db.Migrator().HasColumn(table, "aborted"))
	require.NoError(t, withMigrationLimits(db, func(tx *gorm.DB) error {
		return tx.Exec("ALTER TABLE " + table + " ALTER COLUMN label SET DEFAULT ''").Error
	}))
	var lockTimeout string
	require.NoError(t, db.Raw("SHOW lock_timeout").Scan(&lockTimeout).Error)
	require.Equal(t, "0", lockTimeout, "migration timeout must not leak into the pool")
	// Exercise actual GORM log-schema migration and a repeat startup on PostgreSQL.
	require.NoError(t, autoMigrateWithLimits(db.Table(table), &Log{}))
	require.NoError(t, autoMigrateWithLimits(db.Table(table), &Log{}))
	require.True(t, db.Migrator().HasIndex(table, "idx_logs_user_request_type_id"))
}
