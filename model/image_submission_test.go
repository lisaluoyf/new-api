package model

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestImageSubmissionCrossDBClaimRestartAndLongResponse(t *testing.T) {
	old := DB
	dsn := os.Getenv("IMAGE_SUBMISSION_TEST_PG_DSN")
	var db *gorm.DB
	var err error
	if dsn != "" {
		admin, e := gorm.Open(postgres.Open(dsn), &gorm.Config{})
		require.NoError(t, e)
		schema := "image_submission_" + strings.ReplaceAll(common.GetUUID(), "-", "")
		require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
		dsn += " search_path=" + schema
		db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
		t.Cleanup(func() {
			require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
			sqlDB, _ := admin.DB()
			sqlDB.Close()
		})
	} else {
		dsn = filepath.Join(t.TempDir(), "image.db")
		db, err = gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	}
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	if db.Dialector.Name() == "sqlite" {
		sqlDB.SetMaxOpenConns(1)
	}
	require.NoError(t, db.AutoMigrate(&ImageSubmission{}))
	DB = db
	t.Cleanup(func() { DB = old; sqlDB.Close() })
	var fresh atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, owner, e := ClaimImageSubmission(&ImageSubmission{ID: "one", UserID: 7, Fingerprint: "exact-body"})
			if e != nil {
				errs <- e
			}
			if owner {
				fresh.Add(1)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	require.EqualValues(t, 1, fresh.Load())
	row, err := GetImageSubmission(7, "one")
	require.NoError(t, err)
	require.NoError(t, BeginImageSubmission(row))
	require.ErrorIs(t, BeginImageSubmission(row), ErrImageSubmissionClaimLost)
	// Exceeds MySQL TEXT's 64 KiB limit; the response column must support it.
	body := []byte(fmt.Sprintf(`{"data":[{"b64_json":"%s"}]}`, strings.Repeat("A", 100000)))
	require.NoError(t, FinishImageSubmission(row, "accepted", "original-task", "application/json", 200, body, true))
	require.ErrorIs(t, FinishImageSubmission(row, "acceptance_unknown", "", "", 500, nil, false), ErrImageSubmissionClaimLost)
	var reopened *gorm.DB
	if db.Dialector.Name() == "postgres" {
		reopened, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	} else {
		reopened, err = gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	}
	require.NoError(t, err)
	secondSQL, _ := reopened.DB()
	defer secondSQL.Close()
	DB = reopened
	saved, owner, err := ClaimImageSubmission(&ImageSubmission{ID: "one", UserID: 7, Fingerprint: "exact-body"})
	require.NoError(t, err)
	require.False(t, owner)
	require.Equal(t, string(body), string(saved.ResponseBody))
	require.Equal(t, "accepted", saved.Status)
	_, _, err = ClaimImageSubmission(&ImageSubmission{ID: "one", UserID: 7, Fingerprint: "changed-body"})
	require.ErrorIs(t, err, ErrImageSubmissionConflict)
}
func TestImageSubmissionResponseColumnDialect(t *testing.T) {
	for _, tc := range []struct {
		dialector gorm.Dialector
		want      string
	}{{sqlite.Open(":memory:"), "TEXT"}, {postgres.Open(""), "TEXT"}, {mysql.New(mysql.Config{SkipInitializeWithVersion: true}), "LONGTEXT"}} {
		db := &gorm.DB{Config: &gorm.Config{Dialector: tc.dialector}}
		require.Equal(t, tc.want, ImageSubmissionBody("").GormDBDataType(db, nil))
	}
}
