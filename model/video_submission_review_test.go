package model

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestVideoSubmissionReviewReadOnlyAndHistoricalIsolation(t *testing.T) {
	submissionTestDB(t)
	now := time.Now().Unix()
	for _, id := range []string{"due", "recent", "historical", "ready", "final"} {
		row := VideoSubmission{ID: id, UserID: 7, TaskID: "task_" + id, Status: VideoSubmissionUnknown, CreatedAt: now - 3600, UpdatedAt: now - 120, OwnerToken: "private-owner", Fingerprint: "private"}
		row.ProviderLookupEnabled = id != "historical"
		if id == "recent" {
			row.CreatedAt = now - 60
		}
		if id == "ready" {
			row.TaskReady = true
		}
		if id == "final" {
			row.Status = VideoSubmissionNotAccepted
		}
		require.NoError(t, DB.Create(&row).Error)
	}
	var before []VideoSubmission
	require.NoError(t, DB.Order("id").Find(&before).Error)
	rows, err := ListVideoSubmissionReviews(now-1800, 0, 51)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "due", rows[0].ID)
	// Receipt reads do not expire the owner or restore tasks on GET.
	row, err := ReadVideoSubmission(7, "due")
	require.NoError(t, err)
	require.Equal(t, VideoSubmissionUnknown, row.Status)
	_, err = ReadVideoSubmission(8, "due")
	require.Error(t, err)
	require.Error(t, RecordVideoFeeLookupOutcome(row, now, "secret-provider-error"))
	var after []VideoSubmission
	require.NoError(t, DB.Order("id").Find(&after).Error)
	require.Equal(t, before, after)
}
