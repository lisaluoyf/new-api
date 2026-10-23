package service

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestEvidenceNotificationFirstSeenSurgeAndCooldown(t *testing.T) {
	db := videoVerificationTestDB(t)
	now := int64(1800000000)
	first := model.VideoVerificationAlert{TaskID: 1, ChannelID: 7, Model: "seedance-2.0", Reason: "incomplete_evidence", DetectedAt: now}
	require.NoError(t, prepareIncompleteEvidenceAlert(db, &first))
	require.Equal(t, evidenceFirst, first.Reason)
	require.Equal(t, "pending", first.Status)
	require.NoError(t, db.Create(&first).Error)
	ordinary := model.VideoVerificationAlert{ChannelID: 7, Model: "seedance-2.0", Reason: "incomplete_evidence", DetectedAt: now + 1}
	require.NoError(t, prepareIncompleteEvidenceAlert(db, &ordinary))
	require.Equal(t, "skipped", ordinary.Status)
	// First notice is older than the six-hour cooldown. Nine existing hourly
	// events plus the current video meet the ten-event threshold.
	for i := 0; i < 9; i++ {
		require.NoError(t, db.Create(&model.VideoVerificationAlert{TaskID: int64(i + 2), ChannelID: 7, Model: "seedance-2.0", Reason: "incomplete_evidence", DetectedAt: now + 7*3600 - 60, Status: "skipped"}).Error)
	}
	surge := model.VideoVerificationAlert{TaskID: 20, ChannelID: 7, Model: "seedance-2.0", Reason: "incomplete_evidence", DetectedAt: now + 7*3600}
	require.NoError(t, prepareIncompleteEvidenceAlert(db, &surge))
	require.Equal(t, evidenceSurge, surge.Reason)
	require.NoError(t, db.Create(&surge).Error)
	ordinary.DetectedAt = surge.DetectedAt + 1
	require.NoError(t, prepareIncompleteEvidenceAlert(db, &ordinary))
	require.Equal(t, "skipped", ordinary.Status)
	other := model.VideoVerificationAlert{ChannelID: 8, Model: "seedance-2.0", Reason: "incomplete_evidence", DetectedAt: now}
	require.NoError(t, prepareIncompleteEvidenceAlert(db, &other))
	require.Equal(t, evidenceFirst, other.Reason)
}

func TestEvidenceDailyDigestDeduplicatesAndKeepsDayBoundaries(t *testing.T) {
	db := videoVerificationTestDB(t)
	now := time.Date(2026, 10, 23, 1, 0, 0, 0, time.UTC)
	for i, check := range []string{`[{"id":"frames","status":"skip"}]`, `[{"id":"frames","status":"skip"}]`, `[{"id":"frames","status":"pass","value":"97"}]`} {
		require.NoError(t, db.Create(&model.VideoVerificationAlert{TaskID: int64(i + 1), ChannelID: 7, Model: "seedance-2.0", Reason: "incomplete_evidence", DetectedAt: now.Add(-2 * time.Hour).Unix(), VideoChecksJSON: check, Status: "skipped"}).Error)
	}
	require.NoError(t, db.Create(&model.VideoVerificationAlert{TaskID: 4, ChannelID: 8, Model: "seedance-2.0", Reason: "incomplete_evidence", DetectedAt: now.Unix(), Status: "skipped"}).Error)
	require.NoError(t, enqueueEvidenceDailyDigest(db, now.Add(-time.Hour)))
	var count int64
	require.NoError(t, db.Model(&model.VideoVerificationAlert{}).Where("reason = ?", evidenceDigest).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, enqueueEvidenceDailyDigest(db, now))
	require.NoError(t, enqueueEvidenceDailyDigest(db, now))
	var digest model.VideoVerificationAlert
	require.NoError(t, db.Where("reason = ?", evidenceDigest).First(&digest).Error)
	require.True(t, isEvidenceDigest(&digest))
	require.Equal(t, "pending", digest.Status)
	require.Contains(t, digest.VideoChecksJSON, "共 3 个视频、2 类缺口")
	require.NotContains(t, digest.VideoChecksJSON, "渠道 #8")
	require.NoError(t, db.Model(&model.VideoVerificationAlert{}).Where("reason = ?", evidenceDigest).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestEvidenceCompletionKeepsResultWhileMutingRepeatedAlerts(t *testing.T) {
	db := videoVerificationTestDB(t)
	for i := int64(1); i <= 2; i++ {
		task := &model.Task{ID: i, ChannelId: 7}
		state, claimed, err := claimVideoVerification(context.Background(), task, "seedance-2.0", 1000, 1000)
		require.NoError(t, err)
		require.True(t, claimed)
		require.NoError(t, finishVideoVerification(context.Background(), state, task, "notcomplete", "incomplete_evidence", []model.VideoFingerprintCheck{{ID: "frames", Status: "skip", Value: "193"}}, 1001))
	}
	var alerts []model.VideoVerificationAlert
	require.NoError(t, db.Order("id ASC").Find(&alerts).Error)
	require.Len(t, alerts, 2)
	require.Equal(t, evidenceFirst, alerts[0].Reason)
	require.Equal(t, "pending", alerts[0].Status)
	require.Equal(t, "incomplete_evidence", alerts[1].Reason)
	require.Equal(t, "skipped", alerts[1].Status)
	require.Equal(t, alerts[0].VideoChecksJSON, alerts[1].VideoChecksJSON)
	var run model.VideoVerificationRun
	require.NoError(t, db.Where("task_id = ?", 2).First(&run).Error)
	require.Equal(t, "notcomplete", run.Status)
}

func TestEvidenceSurgeRequiresMinimumAndDoubling(t *testing.T) {
	for _, previous := range []int{0, 6} {
		db := videoVerificationTestDB(t)
		now := int64(1800000000)
		require.NoError(t, db.Create(&model.VideoVerificationAlert{TaskID: 1, ChannelID: 7, Model: "seedance-2.0", Reason: "incomplete_evidence", DetectedAt: now - 86400}).Error)
		for i := 0; i < previous; i++ {
			require.NoError(t, db.Create(&model.VideoVerificationAlert{TaskID: int64(i + 2), ChannelID: 7, Model: "seedance-2.0", Reason: "incomplete_evidence", DetectedAt: now - 4000}).Error)
		}
		current := 8
		if previous > 0 {
			current = 9
		}
		for i := 0; i < current; i++ {
			require.NoError(t, db.Create(&model.VideoVerificationAlert{TaskID: int64(i + 100), ChannelID: 7, Model: "seedance-2.0", Reason: "incomplete_evidence", DetectedAt: now - 60}).Error)
		}
		alert := model.VideoVerificationAlert{ChannelID: 7, Model: "seedance-2.0", Reason: "incomplete_evidence", DetectedAt: now}
		require.NoError(t, prepareIncompleteEvidenceAlert(db, &alert))
		require.Equal(t, "skipped", alert.Status)
	}
}
