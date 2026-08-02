package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupUserLogVisibilityTestDB(t *testing.T) {
	t.Helper()
	oldLogDB := LOG_DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Log{}))
	LOG_DB = db
	t.Cleanup(func() {
		LOG_DB = oldLogDB
	})
}

func insertVisibilityTestLogs(t *testing.T) {
	t.Helper()
	logs := []Log{
		{UserId: 1, Type: LogTypeError, RequestId: "recovered", Content: "recovered error 1", CreatedAt: 1},
		{UserId: 1, Type: LogTypeError, RequestId: "recovered", Content: "recovered error 2", CreatedAt: 2},
		{UserId: 1, Type: LogTypeConsume, RequestId: "recovered", Content: "recovered success", CreatedAt: 3},
		{UserId: 1, Type: LogTypeError, RequestId: "failed", Content: "failed error 1", CreatedAt: 4},
		{UserId: 1, Type: LogTypeError, RequestId: "failed", Content: "failed error 2", CreatedAt: 5},
		{UserId: 1, Type: LogTypeError, RequestId: "single", Content: "single error", CreatedAt: 6},
		{UserId: 1, Type: LogTypeError, RequestId: "", Content: "legacy error 1", CreatedAt: 7},
		{UserId: 1, Type: LogTypeError, RequestId: "", Content: "legacy error 2", CreatedAt: 8},
		{UserId: 2, Type: LogTypeConsume, RequestId: "isolated", Content: "other user success", CreatedAt: 9},
		{UserId: 1, Type: LogTypeError, RequestId: "isolated", Content: "isolated user error", CreatedAt: 10},
		{UserId: 1, Type: LogTypeError, RequestId: "violation", Content: "violation final error", CreatedAt: 11},
		{UserId: 1, Type: LogTypeConsume, RequestId: "violation", Content: "violation fee", Other: `{"violation_fee":true}`, CreatedAt: 12},
	}
	require.NoError(t, LOG_DB.Create(&logs).Error)
}

func logContents(logs []*Log) []string {
	contents := make([]string, 0, len(logs))
	for _, log := range logs {
		contents = append(contents, log.Content)
	}
	return contents
}

func TestGetUserLogsCollapsesFallbackErrors(t *testing.T) {
	setupUserLogVisibilityTestDB(t)
	insertVisibilityTestLogs(t)

	logs, total, err := GetUserLogs(1, LogTypeUnknown, 0, 0, "", "", 0, 20, "", "")
	require.NoError(t, err)
	require.EqualValues(t, 8, total)
	require.Equal(t, []string{
		"violation fee",
		"violation final error",
		"isolated user error",
		"legacy error 2",
		"legacy error 1",
		"single error",
		"failed error 2",
		"recovered success",
	}, logContents(logs))
}

func TestGetUserLogsErrorFilterOnlyReturnsFinalFailures(t *testing.T) {
	setupUserLogVisibilityTestDB(t)
	insertVisibilityTestLogs(t)

	logs, total, err := GetUserLogs(1, LogTypeError, 0, 0, "", "", 0, 20, "", "")
	require.NoError(t, err)
	require.Equal(t, []string{
		"violation final error",
		"isolated user error",
		"legacy error 2",
		"legacy error 1",
		"single error",
		"failed error 2",
	}, logContents(logs))
	require.EqualValues(t, 6, total)
}

func TestGetUserLogsPaginationUsesCollapsedTotal(t *testing.T) {
	setupUserLogVisibilityTestDB(t)
	insertVisibilityTestLogs(t)

	logs, total, err := GetUserLogs(1, LogTypeUnknown, 0, 0, "", "", 1, 2, "", "")
	require.NoError(t, err)
	require.EqualValues(t, 8, total)
	require.Equal(t, []string{"violation final error", "isolated user error"}, logContents(logs))
}

func TestGetAllLogsKeepsEveryFallbackAttemptForAdmins(t *testing.T) {
	setupUserLogVisibilityTestDB(t)
	insertVisibilityTestLogs(t)

	logs, total, err := GetAllLogs(LogTypeUnknown, 0, 0, "", "", "", 0, 20, 0, "", "recovered", 1)
	require.NoError(t, err)
	require.EqualValues(t, 3, total)
	require.Equal(t, []string{
		"recovered success",
		"recovered error 2",
		"recovered error 1",
	}, logContents(logs))
}

func TestGetUserLogsHidesRetryableAttemptBeforeOutcome(t *testing.T) {
	for _, other := range []string{
		`{"admin_info":{"retry_decision":{"should_retry":true}}}`,
		`{"admin_info":{"retry_decision":{"should_retry": true}}}`,
	} {
		t.Run(other, func(t *testing.T) {
			setupUserLogVisibilityTestDB(t)
			retryable := Log{
				UserId:    1,
				Type:      LogTypeError,
				RequestId: "retry-in-progress",
				Content:   "transient upstream error",
				Other:     other,
				CreatedAt: 1,
			}
			require.NoError(t, LOG_DB.Create(&retryable).Error)

			logs, total, err := GetUserLogs(1, LogTypeUnknown, 0, 0, "", "", 0, 20, "", retryable.RequestId)
			require.NoError(t, err)
			require.Zero(t, total)
			require.Empty(t, logs)
		})
	}
}

func TestGetUserLogsShowsFinalFailureWithRetryDecision(t *testing.T) {
	setupUserLogVisibilityTestDB(t)
	finalFailure := Log{
		UserId:    1,
		Type:      LogTypeError,
		RequestId: "final-failure",
		Content:   "final upstream error",
		Other:     `{"admin_info":{"retry_decision":{"should_retry":false}}}`,
		CreatedAt: 1,
	}
	require.NoError(t, LOG_DB.Create(&finalFailure).Error)

	logs, total, err := GetUserLogs(1, LogTypeError, 0, 0, "", "", 0, 20, "", finalFailure.RequestId)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, []string{"final upstream error"}, logContents(logs))
}

func TestGetUserLogsRetryableAttemptStaysHiddenAfterSuccess(t *testing.T) {
	setupUserLogVisibilityTestDB(t)
	requestId := "retry-then-success"
	retryable := Log{
		UserId:    1,
		Type:      LogTypeError,
		RequestId: requestId,
		Content:   "transient upstream error",
		Other:     `{"admin_info":{"retry_decision":{"should_retry":true}}}`,
		CreatedAt: 1,
	}
	require.NoError(t, LOG_DB.Create(&retryable).Error)

	logs, total, err := GetUserLogs(1, LogTypeUnknown, 0, 0, "", "", 0, 20, "", requestId)
	require.NoError(t, err)
	require.Zero(t, total)
	require.Empty(t, logs)

	success := Log{UserId: 1, Type: LogTypeConsume, RequestId: requestId, Content: "success", CreatedAt: 2}
	require.NoError(t, LOG_DB.Create(&success).Error)

	logs, total, err = GetUserLogs(1, LogTypeUnknown, 0, 0, "", "", 0, 20, "", requestId)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, []string{"success"}, logContents(logs))
}

func TestGetAllLogsKeepsRetryableAttemptForAdmins(t *testing.T) {
	setupUserLogVisibilityTestDB(t)
	retryable := Log{
		UserId:    1,
		Type:      LogTypeError,
		RequestId: "admin-retry-in-progress",
		Content:   "transient upstream error",
		Other:     `{"admin_info":{"retry_decision":{"should_retry":true}}}`,
		CreatedAt: 1,
	}
	require.NoError(t, LOG_DB.Create(&retryable).Error)

	logs, total, err := GetAllLogs(LogTypeUnknown, 0, 0, "", "", "", 0, 20, 0, "", retryable.RequestId, 1)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, []string{"transient upstream error"}, logContents(logs))
}
