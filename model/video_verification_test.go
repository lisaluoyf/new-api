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
	require.Empty(t, NormalizeVerifiedVideoModel("seedance-2.0-fast"))
}

func TestVideoVerificationStoresActualProviderKeyPrivately(t *testing.T) {
	task := InitTask(constant.TaskPlatformApimartVideo, &relaycommon.RelayInfo{OriginModelName: "seedance-2.0", ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 7, ChannelType: constant.ChannelTypeOpenAI, ApiKey: "actual-used-key"}})
	require.Equal(t, "actual-used-key", task.PrivateData.Key)
	require.Equal(t, 7, task.ChannelId)
}

func TestVideoVerificationNotificationOnlyAfterCommittedSuccess(t *testing.T) {
	db := webhookTestDB(t)
	queue := make(chan VideoVerificationEvent, 8)
	SetVideoVerificationQueue(queue)
	t.Cleanup(func() { SetVideoVerificationQueue(nil) })
	task := Task{TaskID: GenerateTaskID(), ChannelId: 7, Status: TaskStatusSubmitted, Properties: Properties{OriginModelName: "seedance-2.0"}}
	require.NoError(t, task.Insert())
	require.Empty(t, queue)
	task.Status = TaskStatusSuccess
	won, err := task.UpdateWithStatus(TaskStatusSubmitted)
	require.NoError(t, err)
	require.True(t, won)
	require.Len(t, queue, 1)
	event := <-queue
	var persisted Task
	require.NoError(t, db.First(&persisted, event.TaskID).Error)
	require.Equal(t, TaskStatus(TaskStatusSuccess), persisted.Status)
	won, err = task.UpdateWithStatus(TaskStatusSubmitted)
	require.NoError(t, err)
	require.False(t, won)
	require.Empty(t, queue)

	failed := Task{TaskID: GenerateTaskID(), ChannelId: 7, Status: TaskStatusSubmitted, Properties: Properties{OriginModelName: "seedance-2.0"}, PrivateData: TaskPrivateData{Webhook: &TaskWebhookConfig{EndpointID: "endpoint"}}}
	require.NoError(t, failed.Insert())
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("reject_video_webhook", func(tx *gorm.DB) {
		if tx.Statement.Table == "task_webhook_events" {
			tx.AddError(errors.New("webhook transaction failed"))
		}
	}))
	failed.Status = TaskStatusSuccess
	_, err = failed.UpdateWithStatus(TaskStatusSubmitted)
	require.Error(t, err)
	require.Empty(t, queue)
}

func TestVideoVerificationNotificationBulkAndQueueFull(t *testing.T) {
	webhookTestDB(t)
	queue := make(chan VideoVerificationEvent, 1)
	SetVideoVerificationQueue(queue)
	t.Cleanup(func() { SetVideoVerificationQueue(nil) })
	first := Task{TaskID: GenerateTaskID(), ChannelId: 7, Status: TaskStatusSubmitted, Properties: Properties{OriginModelName: "seedance-2.0"}}
	second := first
	second.TaskID = GenerateTaskID()
	require.NoError(t, first.Insert())
	require.NoError(t, second.Insert())
	require.NoError(t, TaskBulkUpdateByID([]int64{first.ID, second.ID}, map[string]any{"status": TaskStatusSuccess}))
	require.Len(t, queue, 1)
	<-queue
	require.NoError(t, TaskBulkUpdateByID([]int64{first.ID, second.ID}, map[string]any{"status": TaskStatusSuccess}))
	require.Empty(t, queue)
	variant := Task{TaskID: GenerateTaskID(), ChannelId: 7, Status: TaskStatusSuccess, Properties: Properties{OriginModelName: "seedance-2.0-fast"}}
	require.NoError(t, variant.Insert())
	require.Empty(t, queue)
}
