package model

import (
	"os"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// SQLite always runs in CI. PostgreSQL/MySQL run when their integration DSNs
// are supplied, using the exact same GORM migration and upsert path.
func TestFreeModelMemberMigrationAcrossSupportedDatabases(t *testing.T) {
	tests := []struct {
		name   string
		dsnEnv string
		open   func(string) gorm.Dialector
	}{
		{"sqlite", "", func(dsn string) gorm.Dialector { return sqlite.Open(dsn) }},
		{"postgres", "TEST_POSTGRES_DSN", func(dsn string) gorm.Dialector { return postgres.Open(dsn) }},
		{"mysql", "TEST_MYSQL_DSN", func(dsn string) gorm.Dialector { return mysql.Open(dsn) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dsn := "file:free_model_member_migration?mode=memory&cache=shared"
			if test.dsnEnv != "" {
				dsn = os.Getenv(test.dsnEnv)
				if dsn == "" {
					t.Skip(test.dsnEnv + " is not configured")
				}
			}
			db, err := gorm.Open(test.open(dsn), &gorm.Config{})
			require.NoError(t, err)
			require.NoError(t, db.AutoMigrate(&FreeModelMember{}))
			row := DefaultFreeModelMember(910001)
			row.Enabled, row.Text, row.Priority, row.DailyRequestLimit = false, false, 0, 190
			row.Responses = boolPointer(false)
			row.RequiredToolCall = boolPointer(false)
			old := DB
			DB = db
			defer func() { DB = old }()
			require.NoError(t, UpsertFreeModelMember(row))
			stored, exists, err := GetFreeModelMember(row.ChannelID)
			require.NoError(t, err)
			require.True(t, exists)
			require.False(t, stored.Enabled)
			require.False(t, stored.Text)
			require.Zero(t, stored.Priority)
			require.Equal(t, 190, stored.DailyRequestLimit)
			require.False(t, stored.SupportsResponses())
			require.True(t, stored.SupportsChatCompletions())
			require.False(t, stored.SupportsRequiredToolCall())
		})
	}
}
