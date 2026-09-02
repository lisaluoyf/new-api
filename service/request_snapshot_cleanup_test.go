package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestRunFailedRequestSnapshotCleanupOnce(t *testing.T) {
	withSnapshotTestDB(t)
	t.Setenv("FAILED_REQUEST_SNAPSHOT_RETENTION_DAYS", "7")

	now := time.Unix(1_800_000_000, 0)
	previousNow := snapshotCleanupNow
	snapshotCleanupNow = func() time.Time { return now }
	t.Cleanup(func() { snapshotCleanupNow = previousNow })

	require.NoError(t, model.DB.Create(&model.FailedRequestSnapshot{RequestId: "expired", CreatedAt: now.Add(-8 * 24 * time.Hour).Unix()}).Error)
	require.NoError(t, model.DB.Create(&model.FailedRequestSnapshot{RequestId: "retained", CreatedAt: now.Add(-6 * 24 * time.Hour).Unix()}).Error)

	runFailedRequestSnapshotCleanupOnce()

	var rows []model.FailedRequestSnapshot
	require.NoError(t, model.DB.Order("request_id").Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Equal(t, "retained", rows[0].RequestId)
}
