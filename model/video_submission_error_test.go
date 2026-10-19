package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestVideoSubmissionFailureEvidencePreservesAcceptanceAndOwnership(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	old := DB
	DB = db
	t.Cleanup(func() { DB = old; sqlDB, _ := db.DB(); sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&VideoSubmission{}))
	row := VideoSubmission{ID: "audit-submission", OwnerToken: "owner", Status: VideoSubmissionSubmitting}
	require.NoError(t, db.Create(&row).Error)
	require.NoError(t, RecordVideoSubmissionError(row.ID, "wrong-owner", 400, "wrong", "wrong", ""))
	require.NoError(t, db.First(&row, "id = ?", row.ID).Error)
	require.Empty(t, row.ErrorCode)
	require.NoError(t, RecordVideoSubmissionError(row.ID, "owner", 502, "content_policy_violation", strings.Repeat("字", 1100), "provider-ref"))
	require.NoError(t, db.First(&row, "id = ?", row.ID).Error)
	require.Equal(t, VideoSubmissionSubmitting, row.Status)
	require.Equal(t, 502, row.ErrorStatus)
	require.Len(t, []rune(row.ErrorMessage), 1024)
	require.Equal(t, "provider-ref", row.ErrorReference)
	require.Positive(t, row.ErrorRecordedAt)
	raw, err := common.Marshal(row)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "provider-ref")
	require.NoError(t, db.Model(&row).Updates(map[string]any{"status": VideoSubmissionAccepted, "upstream_task_id": "original"}).Error)
	require.NoError(t, RecordVideoSubmissionError(row.ID, "owner", 500, "late_error", "late", "late"))
	require.NoError(t, db.First(&row, "id = ?", row.ID).Error)
	require.Equal(t, VideoSubmissionAccepted, row.Status)
	require.Equal(t, "content_policy_violation", row.ErrorCode)
	require.Equal(t, "original", row.UpstreamTaskID)
}
