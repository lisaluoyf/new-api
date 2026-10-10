package model

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Only an isolated development database may be used here.
func TestAdminLogQueryCancelsPostgresLockWait(t *testing.T) {
	dsn := os.Getenv("APIMASTER_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("APIMASTER_TEST_POSTGRES_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	defer pool.Close()
	require.NoError(t, db.AutoMigrate(&Log{}))
	defer db.Migrator().DropTable(&Log{})
	oldLogDB := LOG_DB
	LOG_DB = db
	defer func() { LOG_DB = oldLogDB }()
	blocker := db.Begin()
	require.NoError(t, blocker.Error)
	defer blocker.Rollback()
	require.NoError(t, blocker.Exec("LOCK TABLE logs IN ACCESS EXCLUSIVE MODE").Error)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err = GetAllLogsWithContext(ctx, LogTypeUnknown, 0, 0, "", "", "", 0, 10, 0, "", "missing", 0)
	require.Error(t, err)
	require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
	require.Less(t, time.Since(start), 2*time.Second)
	require.NoError(t, blocker.Rollback().Error)
	// The canceled query must not leave its connection/transaction blocked.
	require.NoError(t, db.Exec("SET statement_timeout = '2s'").Error)
	require.NoError(t, db.Exec("INSERT INTO logs (request_id) VALUES ('after-cancel')").Error)
}
