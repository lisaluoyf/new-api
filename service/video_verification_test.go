package service

import (
	"context"
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func videoVerificationTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	previous := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previous; sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&model.VideoVerificationState{}, &model.VideoVerificationRun{}, &model.VideoVerificationAlert{}, &model.ChannelDetectLog{}))
	return db
}

func TestVideoVerificationWorkerPanicDoesNotEscape(t *testing.T) {
	require.NotPanics(t, func() {
		runVideoVerificationSafely(func() { panic("private error must not escape or be logged") })
	})
}

func TestVideoVerificationEveryVideoAndDeduplication(t *testing.T) {
	db := videoVerificationTestDB(t)
	ctx := context.Background()
	task := &model.Task{ID: 100, ChannelId: 7, Properties: model.Properties{OriginModelName: "seedance-2.0"}}
	state, claimed, err := claimVideoVerification(ctx, task, "seedance-2.0", 1000, 1000)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, finishVideoVerification(ctx, state, task, "notcomplete", "unknown_fingerprint", nil, 1001))
	_, claimed, err = claimVideoVerification(ctx, task, "seedance-2.0", 1000, 2000)
	require.NoError(t, err)
	require.False(t, claimed)

	task.ID = 101
	state, claimed, err = claimVideoVerification(ctx, task, "seedance-2.0", 1600, 2000)
	require.NoError(t, err)
	require.True(t, claimed, "every completed video must be checked even within ten minutes")
	require.NoError(t, finishVideoVerification(ctx, state, task, "notcomplete", "unknown_fingerprint", nil, 2001))

	task.ID = 99
	state, claimed, err = claimVideoVerification(ctx, task, "seedance-2.0", 1601, 2000)
	require.NoError(t, err)
	require.True(t, claimed, "a task created earlier but completed after the window is eligible")
	require.NoError(t, finishVideoVerification(ctx, state, task, "notcomplete", "download_failed", nil, 2001))
	var count int64
	require.NoError(t, db.Model(&model.VideoVerificationRun{}).Count(&count).Error)
	require.EqualValues(t, 3, count, "one durable run per completed video")
}

func TestVideoVerificationRouteIsolationAndLeaseRecovery(t *testing.T) {
	videoVerificationTestDB(t)
	ctx := context.Background()
	task := &model.Task{ID: 1, ChannelId: 7}
	_, claimed, err := claimVideoVerification(ctx, task, "seedance-2.0", 1000, 1000)
	require.NoError(t, err)
	require.True(t, claimed)
	_, claimed, err = claimVideoVerification(ctx, task, "seedance-2.0", 1000, 1100)
	require.Error(t, err)
	require.False(t, claimed)
	_, claimed, err = claimVideoVerification(ctx, task, "seedance-2.0", 1000, 1300)
	require.NoError(t, err)
	require.True(t, claimed)

	task.ID = 2
	_, claimed, err = claimVideoVerification(ctx, task, "seedance-2.5", 1100, 1100)
	require.NoError(t, err)
	require.True(t, claimed)
	task.ID = 3
	task.ChannelId = 8
	_, claimed, err = claimVideoVerification(ctx, task, "seedance-2.0", 1100, 1100)
	require.NoError(t, err)
	require.True(t, claimed)
}

func TestVideoVerificationAlertIsDurableAndRetryable(t *testing.T) {
	db := videoVerificationTestDB(t)
	task := &model.Task{ID: 1, ChannelId: 7, Properties: model.Properties{OriginModelName: "seedance-2.0"}}
	state, claimed, err := claimVideoVerification(context.Background(), task, "seedance-2.0", 1000, 1000)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, finishVideoVerification(context.Background(), state, task, "suspicious", "version_mismatch", nil, 1001))
	var alert model.VideoVerificationAlert
	require.NoError(t, db.First(&alert).Error)
	require.Equal(t, "pending", alert.Status)
	previous := sendVideoVerificationAlert
	t.Cleanup(func() { sendVideoVerificationAlert = previous })
	sendVideoVerificationAlert = func(*model.VideoVerificationAlert) error { return errors.New("unavailable") }
	require.NoError(t, deliverDueVideoVerificationAlerts())
	require.NoError(t, db.First(&alert).Error)
	require.Equal(t, 1, alert.Attempts)
	require.Equal(t, "pending", alert.Status)
	require.Greater(t, alert.NextAt, int64(1001))
	sendVideoVerificationAlert = func(*model.VideoVerificationAlert) error { return nil }
	require.NoError(t, db.Model(&alert).Update("next_at", 0).Error)
	require.NoError(t, deliverDueVideoVerificationAlerts())
	require.NoError(t, db.First(&alert).Error)
	require.Equal(t, "sent", alert.Status)
}

func TestVideoVerificationSkipsQueuedMiniAndFast(t *testing.T) {
	for _, name := range []string{"seedance-2.0-mini", "seedance-2.0-fast", "doubao-seedance-2.0-mini", "doubao-seedance-2.0-fast"} {
		for _, status := range []string{"pending", "processing"} {
			t.Run(name+"/"+status, func(t *testing.T) {
				db := videoVerificationTestDB(t)
				require.NoError(t, db.AutoMigrate(&model.Task{}))
				task := model.Task{ID: 1, ChannelId: 153, Status: model.TaskStatusSuccess, Properties: model.Properties{OriginModelName: name}}
				require.NoError(t, db.Create(&task).Error)
				run := model.VideoVerificationRun{TaskID: task.ID, ChannelID: task.ChannelId, Model: name, Status: status}
				require.NoError(t, db.Create(&run).Error)
				require.True(t, handleVideoVerificationEvent(task.ID, 1000))
				require.NoError(t, db.First(&run).Error)
				require.Equal(t, "skipped", run.Status)
				require.Zero(t, run.LeaseUntil)
				var count int64
				for _, table := range []any{&model.VideoVerificationState{}, &model.ChannelDetectLog{}, &model.VideoVerificationAlert{}} {
					require.NoError(t, db.Model(table).Count(&count).Error)
					require.Zero(t, count)
				}
			})
		}
	}
}

func TestVideoVerificationDoesNotNotifyQueuedMiniAndFast(t *testing.T) {
	db := videoVerificationTestDB(t)
	previous := sendVideoVerificationAlert
	t.Cleanup(func() { sendVideoVerificationAlert = previous })
	var sent []string
	sendVideoVerificationAlert = func(alert *model.VideoVerificationAlert) error {
		sent = append(sent, alert.Model)
		return nil
	}
	names := []string{"seedance-2.0-mini", "seedance-2.0-fast", "doubao-seedance-2.0-mini", "doubao-seedance-2.0-fast", "seedance-2.0", "seedance-2.5"}
	for index, name := range names {
		alert := model.VideoVerificationAlert{TaskID: int64(index + 1), Model: name, Reason: "incomplete_evidence", Status: "pending"}
		require.NoError(t, db.Create(&alert).Error)
	}
	require.NoError(t, deliverDueVideoVerificationAlerts())
	require.NoError(t, deliverDueVideoVerificationAlerts())
	require.Equal(t, []string{"seedance-2.0", "seedance-2.5"}, sent)
	var alerts []model.VideoVerificationAlert
	require.NoError(t, db.Order("id ASC").Find(&alerts).Error)
	for _, alert := range alerts[:4] {
		require.Equal(t, "skipped", alert.Status)
		require.Zero(t, alert.Attempts)
		require.Zero(t, alert.LeaseUntil)
	}
}

func TestVideoVerificationDoesNotCreateMiniOrFastAlertOnCompletion(t *testing.T) {
	db := videoVerificationTestDB(t)
	for index, name := range []string{"seedance-2.0-mini", "seedance-2.0-fast"} {
		task := &model.Task{ID: int64(index + 1), ChannelId: 153, Properties: model.Properties{OriginModelName: name}}
		state, claimed, err := claimVideoVerification(context.Background(), task, name, 1000, 1000)
		require.NoError(t, err)
		require.True(t, claimed)
		require.NoError(t, finishVideoVerification(context.Background(), state, task, "notcomplete", "incomplete_evidence", nil, 1001))
	}
	var count int64
	require.NoError(t, db.Model(&model.VideoVerificationAlert{}).Count(&count).Error)
	require.Zero(t, count)
}
