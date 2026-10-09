package model

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPublicVideoFingerprintChecksRequireCompletePassEvidence(t *testing.T) {
	valid := `[{"id":"claim","status":"pass","value":"SD2.0"},{"id":"dimensions","status":"pass","value":"1280x720"},{"id":"family","status":"pass","value":"ark"},{"id":"x264","status":"pass","value":"superfast · crf 20.0"},{"id":"frames","status":"pass","value":"97"}]`
	require.Len(t, PublicVideoFingerprintChecks(valid), 5)
	require.Nil(t, PublicVideoFingerprintChecks(`[{"id":"claim","status":"pass","value":"SD2.0"}]`))
	require.Nil(t, PublicVideoFingerprintChecks(`[{"id":"claim","status":"skip","value":"SD2.0"},{"id":"dimensions","status":"pass","value":"1280x720"},{"id":"family","status":"pass","value":"ark"},{"id":"x264","status":"pass","value":"superfast"},{"id":"frames","status":"pass","value":"97"}]`))
}

func TestNormalizeVerifiedVideoModel(t *testing.T) {
	require.Equal(t, "seedance-2.0", NormalizeVerifiedVideoModel("doubao-seedance-2.0"))
	require.Equal(t, "seedance-2.5", NormalizeVerifiedVideoModel(" Seedance-2.5 "))
	require.Equal(t, "seedance-2.0-fast", NormalizeVerifiedVideoModel("seedance-2.0-fast"))
}

func TestVideoVerificationStoresActualProviderKeyPrivately(t *testing.T) {
	task := InitTask(constant.TaskPlatformApimartVideo, &relaycommon.RelayInfo{OriginModelName: "seedance-2.0", ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 7, ChannelType: constant.ChannelTypeOpenAI, ApiKey: "actual-used-key"}})
	require.Equal(t, "actual-used-key", task.PrivateData.Key)
	require.Equal(t, 7, task.ChannelId)
}

func TestVideoVerificationDurableCompletionAndRollback(t *testing.T) {
	db := webhookTestDB(t)
	task := Task{TaskID: GenerateTaskID(), ChannelId: 7, Status: TaskStatusSubmitted, Properties: Properties{OriginModelName: "seedance-2.5"}}
	require.NoError(t, task.Insert())
	var count int64
	require.NoError(t, db.Model(&VideoVerificationRun{}).Count(&count).Error)
	require.Zero(t, count)
	task.Status = TaskStatusSuccess
	won, err := task.UpdateWithStatus(TaskStatusSubmitted)
	require.NoError(t, err)
	require.True(t, won)
	won, err = task.UpdateWithStatus(TaskStatusSubmitted)
	require.NoError(t, err)
	require.False(t, won)
	require.NoError(t, db.Model(&VideoVerificationRun{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	second := Task{TaskID: GenerateTaskID(), ChannelId: 7, Status: TaskStatusSubmitted, Properties: Properties{OriginModelName: "seedance-2.0-fast"}}
	require.NoError(t, second.Insert())
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("reject_video_outbox", func(tx *gorm.DB) {
		if tx.Statement.Table == "video_verification_runs" {
			tx.AddError(errors.New("outbox failure"))
		}
	}))
	second.Status = TaskStatusSuccess
	_, err = second.UpdateWithStatus(TaskStatusSubmitted)
	require.Error(t, err)
	var persisted Task
	require.NoError(t, db.First(&persisted, second.ID).Error)
	require.Equal(t, TaskStatus(TaskStatusSubmitted), persisted.Status)
}

func TestVideoVerificationDurableBulkAndImmediate(t *testing.T) {
	db := webhookTestDB(t)
	tasks := []Task{}
	for _, name := range []string{"seedance-2.0", "seedance-2.5", "seedance-2.0-fast", "seedance-2.0-mini"} {
		task := Task{TaskID: GenerateTaskID(), ChannelId: 7, Status: TaskStatusSubmitted, Properties: Properties{OriginModelName: name}}
		require.NoError(t, task.Insert())
		tasks = append(tasks, task)
	}
	ids := []int64{}
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	require.NoError(t, TaskBulkUpdateByID(ids, map[string]any{"status": TaskStatusSuccess}))
	require.NoError(t, TaskBulkUpdateByID(ids, map[string]any{"status": TaskStatusSuccess}))
	var count int64
	require.NoError(t, db.Model(&VideoVerificationRun{}).Count(&count).Error)
	require.EqualValues(t, 4, count)
	immediate := Task{TaskID: GenerateTaskID(), ChannelId: 7, Status: TaskStatusSuccess, Properties: Properties{OriginModelName: "seedance-2.5"}}
	require.NoError(t, immediate.Insert())
	require.NoError(t, db.Model(&VideoVerificationRun{}).Count(&count).Error)
	require.EqualValues(t, 5, count)
}
