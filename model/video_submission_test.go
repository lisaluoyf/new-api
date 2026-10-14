package model

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func submissionTestDB(t *testing.T) string {
	t.Helper()
	old := DB
	path := filepath.Join(t.TempDir(), "submissions.db")
	var db *gorm.DB
	var err error
	if dsn := os.Getenv("VIDEO_SUBMISSION_TEST_PG_DSN"); dsn != "" {
		admin, openErr := gorm.Open(postgres.Open(dsn), &gorm.Config{})
		require.NoError(t, openErr)
		schema := "submission_" + strings.ReplaceAll(common.GetUUID(), "-", "")
		require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
		path = dsn + " search_path=" + schema
		db, err = gorm.Open(postgres.Open(path), &gorm.Config{})
		t.Cleanup(func() {
			require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
			sqlAdmin, _ := admin.DB()
			sqlAdmin.Close()
		})
	} else {
		db, err = gorm.Open(sqlite.Open(path), &gorm.Config{})
	}
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	if db.Dialector.Name() == "sqlite" {
		sqlDB.SetMaxOpenConns(1)
	}
	require.NoError(t, db.AutoMigrate(&VideoSubmission{}, &Task{}, &SeedanceBillingReceipt{}, &Token{}))
	DB = db
	t.Cleanup(func() { DB = old; sqlDB.Close() })
	return path
}
func claimFixture(id string) *VideoSubmission {
	return &VideoSubmission{ID: id, UserID: 7, Fingerprint: "payload", Model: "seedance-2.0", TaskID: GenerateTaskID()}
}
func TestVideoSubmissionConcurrentClaimsAndRestart(t *testing.T) {
	path := submissionTestDB(t)
	var fresh atomic.Int32
	var wg sync.WaitGroup
	errors := make(chan error, 32)
	tasks := make(chan string, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, owner, err := ClaimVideoSubmission(claimFixture("one"))
			if err != nil {
				errors <- err
				return
			}
			if owner {
				fresh.Add(1)
			}
			tasks <- r.TaskID
		}()
	}
	wg.Wait()
	close(errors)
	close(tasks)
	for err := range errors {
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, fresh.Load())
	taskID := ""
	for id := range tasks {
		if taskID == "" {
			taskID = id
		}
		require.Equal(t, taskID, id)
	}
	require.NoError(t, MarkVideoSubmissionSending("one", 42))
	require.ErrorIs(t, MarkVideoSubmissionSending("one", 42), ErrVideoSubmissionClaimLost)
	require.NoError(t, FinishVideoSubmission("one", VideoSubmissionUnknown, "", false))
	var db *gorm.DB
	var err error
	if os.Getenv("VIDEO_SUBMISSION_TEST_PG_DSN") != "" {
		db, err = gorm.Open(postgres.Open(path), &gorm.Config{})
	} else {
		db, err = gorm.Open(sqlite.Open(path), &gorm.Config{})
	}
	require.NoError(t, err)
	old := DB
	DB = db
	defer func() { DB = old }()
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	again, owner, err := ClaimVideoSubmission(claimFixture("one"))
	require.NoError(t, err)
	require.False(t, owner)
	require.Equal(t, VideoSubmissionUnknown, again.Status)
	require.Equal(t, taskID, again.TaskID)
	changed := claimFixture("one")
	changed.Fingerprint = "different"
	_, _, err = ClaimVideoSubmission(changed)
	require.ErrorIs(t, err, ErrVideoSubmissionConflict)
	_, err = GetVideoSubmission(8, "one")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}
func TestVideoSubmissionExpiredOwnerCannotSendAndUnknownNeverRequeues(t *testing.T) {
	submissionTestDB(t)
	for _, status := range []string{VideoSubmissionPending, VideoSubmissionSubmitting} {
		r, _, err := ClaimVideoSubmission(claimFixture(status))
		require.NoError(t, err)
		require.NoError(t, DB.Model(r).Updates(map[string]any{"status": status, "deadline": time.Now().Unix() - 1}).Error)
		r, err = GetVideoSubmission(7, status)
		require.NoError(t, err)
		expected := VideoSubmissionNotAccepted
		if status == VideoSubmissionSubmitting {
			expected = VideoSubmissionUnknown
		}
		require.Equal(t, expected, r.Status)
		require.ErrorIs(t, MarkVideoSubmissionSending(r.ID, 1), ErrVideoSubmissionClaimLost)
		_, owner, err := ClaimVideoSubmission(claimFixture(status))
		require.NoError(t, err)
		require.False(t, owner)
	}
	// Late official confirmation can resolve uncertainty, but never cause a new send.
	require.NoError(t, FinishVideoSubmission(VideoSubmissionSubmitting, VideoSubmissionAccepted, "private-provider-id", false))
	require.Error(t, FinishVideoSubmission(VideoSubmissionSubmitting, VideoSubmissionUnknown, "", false))
	r, err := GetVideoSubmission(7, VideoSubmissionSubmitting)
	require.NoError(t, err)
	require.Equal(t, VideoSubmissionAccepted, r.Status)
}
func TestVideoSubmissionTaskAndBillingReceiptAreAtomic(t *testing.T) {
	submissionTestDB(t)
	r, _, err := ClaimVideoSubmission(claimFixture("atomic"))
	require.NoError(t, err)
	require.NoError(t, MarkVideoSubmissionSending(r.ID, 1))
	task := &Task{TaskID: r.TaskID, UserId: 7, PrivateData: TaskPrivateData{UpstreamTaskID: "private-id"}}
	receipt := &SeedanceBillingReceipt{ID: "receipt_atomic", TaskID: r.TaskID, UserID: 7}
	require.NoError(t, DB.Create(receipt).Error)
	require.Error(t, InsertVideoSubmissionTask(task, receipt, r.ID))
	var count int64
	require.NoError(t, DB.Model(&Task{}).Where("task_id = ?", r.TaskID).Count(&count).Error)
	require.Zero(t, count)
	current, err := GetVideoSubmission(7, r.ID)
	require.NoError(t, err)
	require.False(t, current.TaskReady)
	require.NoError(t, DB.Delete(receipt).Error)
	require.NoError(t, InsertVideoSubmissionTask(task, receipt, r.ID))
	current, err = GetVideoSubmission(7, r.ID)
	require.NoError(t, err)
	require.True(t, current.TaskReady)
	require.Equal(t, VideoSubmissionAccepted, current.Status)
}
func TestRecoveryTokenStillRejectsRevokedExpiredAndMissing(t *testing.T) {
	submissionTestDB(t)
	oldKeyCol := commonKeyCol
	commonKeyCol = "`key`"
	if DB.Dialector.Name() == "postgres" {
		commonKeyCol = "\"key\""
	}
	t.Cleanup(func() { commonKeyCol = oldKeyCol })
	for i, status := range []int{common.TokenStatusEnabled, common.TokenStatusExhausted, common.TokenStatusDisabled, common.TokenStatusExpired} {
		key := fmt.Sprintf("recovery%d", i)
		token := &Token{UserId: 7, Key: key, Status: status, RemainQuota: 0, ExpiredTime: -1}
		require.NoError(t, DB.Create(token).Error)
		_, err := ValidateUserTokenForRecovery(key)
		if status == common.TokenStatusEnabled || status == common.TokenStatusExhausted {
			require.NoError(t, err)
		} else {
			require.Error(t, err)
		}
		_, err = ValidateUserToken(key)
		require.Error(t, err)
	}
	require.NoError(t, DB.Create(&Token{UserId: 7, Key: "expired-date", Status: common.TokenStatusEnabled, UnlimitedQuota: true, ExpiredTime: time.Now().Unix() - 60}).Error)
	_, err := ValidateUserTokenForRecovery("expired-date")
	require.Error(t, err)
	_, err = ValidateUserTokenForRecovery("missing")
	require.Error(t, err)
}

func TestRejectedSubmissionResetRequiresLiveOwnerAndUnacceptedAttempt(t *testing.T) {
	submissionTestDB(t)
	record, fresh, err := ClaimVideoSubmission(&VideoSubmission{ID: "retry-proof", UserID: 1, Fingerprint: "same", TaskID: "public-task"})
	require.NoError(t, err)
	require.True(t, fresh)
	require.NoError(t, MarkVideoSubmissionSending(record.ID, 7))
	require.Error(t, RetryRejectedVideoSubmission(record.ID, "wrong-owner", 7))
	require.Error(t, RetryRejectedVideoSubmission(record.ID, record.OwnerToken, 8))
	require.NoError(t, RetryRejectedVideoSubmission(record.ID, record.OwnerToken, 7))
	require.NoError(t, MarkVideoSubmissionSending(record.ID, 8))
	require.NoError(t, FinishVideoSubmission(record.ID, VideoSubmissionAccepted, "accepted-upstream", false))
	require.Error(t, RetryRejectedVideoSubmission(record.ID, record.OwnerToken, 8))
	saved, err := GetVideoSubmission(1, record.ID)
	require.NoError(t, err)
	require.Equal(t, VideoSubmissionAccepted, saved.Status)
	require.Contains(t, saved.AttemptsJSON, "confirmed_not_accepted")
}
