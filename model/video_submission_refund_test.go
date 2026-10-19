package model

import (
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func videoRefundFixture(t *testing.T) *VideoSubmission {
	submissionTestDB(t)
	oldLog := LOG_DB
	LOG_DB = DB
	t.Cleanup(func() { LOG_DB = oldLog })
	require.NoError(t, DB.AutoMigrate(&User{}, &BillingHold{}, &VideoSubmissionRefund{}, &VideoRefundLogDelivery{}, &Log{}))
	require.NoError(t, DB.Create(&User{Id: 7, Username: "video-refund-user", Quota: 100}).Error)
	require.NoError(t, DB.Create(&Token{Id: 23, UserId: 7, Key: "test-video-key", RemainQuota: 60, UsedQuota: 40}).Error)
	row := &VideoSubmission{ID: "video-refund", UserID: 7, TaskID: "task_original", RequestID: "original-request", Model: "seedance-2.5", Status: VideoSubmissionUnknown, ChannelID: 273}
	snap := VideoSubmissionRecovery{Task: Task{TaskID: row.TaskID, UserId: 7, Quota: 40}, PrivateData: TaskPrivateData{TokenId: 23, BillingSource: "wallet"}}
	raw, err := common.Marshal(snap)
	require.NoError(t, err)
	row.RecoveryJSON = string(raw)
	require.NoError(t, DB.Create(row).Error)
	return row
}
func checkVideoRefundBalances(t *testing.T, wallet, tokenRemaining, tokenUsed int) {
	t.Helper()
	var u User
	var token Token
	require.NoError(t, DB.First(&u, 7).Error)
	require.NoError(t, DB.First(&token, 23).Error)
	require.Equal(t, wallet, u.Quota)
	require.Equal(t, tokenRemaining, token.RemainQuota)
	require.Equal(t, tokenUsed, token.UsedQuota)
}
func TestVideoSubmissionRefundAtomicConcurrentAndQueryable(t *testing.T) {
	row := videoRefundFixture(t)
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := RefundVideoSubmissionPreConsume(row, 40, 23, 40, false)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	checkVideoRefundBalances(t, 140, 100, 0)
	var count int64
	require.NoError(t, DB.Model(&Log{}).Where("type = ?", LogTypeRefund).Count(&count).Error)
	require.EqualValues(t, 1, count)
	details, err := GetVideoSubmissionRefundDetails(7, row.TaskID)
	require.NoError(t, err)
	require.Equal(t, "refunded", details["settlement_status"])
	require.Equal(t, "0.000080", details["refunded_amount"])
	require.Equal(t, "acceptance_unknown", details["submission_status"])
	require.Equal(t, false, details["new_submission_safe"])
	_, err = GetVideoSubmissionRefundDetails(8, row.TaskID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = RefundVideoSubmissionPreConsume(row, 41, 23, 40, false)
	require.Error(t, err)
	checkVideoRefundBalances(t, 140, 100, 0)
}
func TestVideoSubmissionRefundRollsBackWithoutAuditLog(t *testing.T) {
	row := videoRefundFixture(t)
	require.NoError(t, DB.Migrator().DropTable(&VideoRefundLogDelivery{}))
	_, err := RefundVideoSubmissionPreConsume(row, 40, 23, 40, false)
	require.Error(t, err)
	checkVideoRefundBalances(t, 100, 60, 40)
	var count int64
	require.NoError(t, DB.Model(&VideoSubmissionRefund{}).Count(&count).Error)
	require.Zero(t, count)
}
func TestVideoSubmissionRefundRollsBackMissingTokenAndRejectsAcceptance(t *testing.T) {
	row := videoRefundFixture(t)
	require.NoError(t, DB.Where("id = ?", 23).Delete(&Token{}).Error)
	_, err := RefundVideoSubmissionPreConsume(row, 40, 23, 40, false)
	require.Error(t, err)
	var u User
	require.NoError(t, DB.First(&u, 7).Error)
	require.Equal(t, 100, u.Quota)
	require.NoError(t, DB.Model(row).Update("status", VideoSubmissionAccepted).Error)
	_, err = RefundVideoSubmissionPreConsume(row, 40, 23, 40, false)
	require.Error(t, err)
}
func TestVideoSubmissionRefundSplitLogDatabaseRetriesWithoutRecredit(t *testing.T) {
	row := videoRefundFixture(t)
	logDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	LOG_DB = logDB
	require.NoError(t, LOG_DB.AutoMigrate(&Log{}))
	_, err = RefundVideoSubmissionPreConsume(row, 40, 23, 40, false)
	require.NoError(t, err)
	checkVideoRefundBalances(t, 140, 100, 0)
	var saved VideoSubmissionRefund
	require.NoError(t, DB.First(&saved, "id = ?", row.ID).Error)
	require.False(t, saved.LogPublished)
	require.NoError(t, LOG_DB.AutoMigrate(&VideoRefundLogDelivery{}))
	require.NoError(t, RetryVideoSubmissionRefundLogs())
	require.NoError(t, RetryVideoSubmissionRefundLogs())
	_, err = RefundVideoSubmissionPreConsume(row, 40, 23, 40, false)
	require.NoError(t, err)
	var count int64
	require.NoError(t, LOG_DB.Model(&Log{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	checkVideoRefundBalances(t, 140, 100, 0)
}
func TestVideoSubmissionHistoricalHoldRefundQueryableWithoutNewCredit(t *testing.T) {
	row := videoRefundFixture(t)
	require.NoError(t, DB.Create(&BillingHold{RequestId: row.RequestID, UserId: row.UserID, Status: BillingHoldStatusRefunded, PreConsumedQuota: 40, ResolvedAt: 1234}).Error)
	d, err := GetVideoSubmissionRefundDetails(7, row.TaskID)
	require.NoError(t, err)
	require.Equal(t, "billing_hold_refund", d["evidence_source"])
	_, err = RefundVideoSubmissionPreConsume(row, 40, 23, 40, false)
	require.Error(t, err)
	checkVideoRefundBalances(t, 100, 60, 40)
}

func TestVideoSubmissionRefundBlocksLateAcceptanceRestoration(t *testing.T) {
	submissionTestDB(t)
	oldLog := LOG_DB
	LOG_DB = DB
	t.Cleanup(func() { LOG_DB = oldLog })
	require.NoError(t, DB.AutoMigrate(&User{}, &VideoSubmissionRefund{}, &VideoRefundLogDelivery{}, &Log{}))
	require.NoError(t, DB.Create(&User{Id: 7, Username: "late-acceptance-user", Quota: 100}).Error)
	require.NoError(t, DB.Create(&Token{Id: 23, UserId: 7, Key: "late-acceptance-key", RemainQuota: 100, UsedQuota: 12345}).Error)
	row, _, _ := recoveryFixture(t, "seedance-2.5", 273)
	_, err := RefundVideoSubmissionPreConsume(row, 12345, 23, 12345, false)
	require.NoError(t, err)
	require.NoError(t, AcceptVideoSubmission(row.ID, "late-original-upstream", []byte(`{"id":"late-original-upstream"}`)))
	require.NoError(t, MarkVideoSubmissionBillingCommitted(row.ID))
	require.NoError(t, ReconcileVideoSubmission(row.UserID, row.ID))
	var count int64
	require.NoError(t, DB.Model(&Task{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, DB.Model(&SeedanceBillingReceipt{}).Count(&count).Error)
	require.Zero(t, count)
	var u User
	require.NoError(t, DB.First(&u, 7).Error)
	require.Equal(t, 12445, u.Quota)
}
