package service

import (
	"context"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

func lateFailureFixture(t *testing.T) (*model.Task, *model.VideoFeeWebhookInbox) {
	t.Helper()
	truncate(t)
	setupTaskWebhookTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.VideoFeeWebhookInbox{}))
	seedUser(t, 91273, 10000)
	seedToken(t, 91273, 91273, "test-customer-key", 10000)
	require.NoError(t, model.DB.Create(&model.Channel{Id: 273, Type: 1, Key: "supplier-key"}).Error)
	task := makeTask(91273, 273, 0, 91273, BillingSourceWallet, 0)
	task.TaskID = "public-late-error"
	task.Platform = constant.TaskPlatformVideoFee
	task.Status = model.TaskStatusFailure
	task.FailReason = "Video generation failed"
	task.FinishTime = 100
	task.Progress = "100%"
	task.PrivateData.UpstreamTaskID = "supplier-late-error"
	require.NoError(t, model.DB.Create(task).Error)
	payload := common.MapToJsonStr(map[string]any{"data": map[string]any{"task_id": task.GetUpstreamTaskID(), "status": "FAILURE", "fail_reason": "InputImageSensitiveContentDetected: The input image may contain sensitive information. Bearer private-secret"}})
	return task, &model.VideoFeeWebhookInbox{ID: "late-event", TaskID: task.ID, Payload: payload}
}

func assertLateFailureNoFinancialEffects(t *testing.T, original *model.Task) model.Task {
	t.Helper()
	var user model.User
	var token model.Token
	var fresh model.Task
	require.NoError(t, model.DB.First(&user, original.UserId).Error)
	require.NoError(t, model.DB.First(&token, 91273).Error)
	require.Equal(t, 10000, user.Quota)
	require.Zero(t, user.UsedQuota)
	require.Equal(t, 10000, token.RemainQuota)
	require.Zero(t, token.UsedQuota)
	for _, table := range []string{"logs", "task_webhook_events"} {
		var count int64
		require.NoError(t, model.DB.Table(table).Count(&count).Error)
		require.Zero(t, count)
	}
	require.NoError(t, model.DB.First(&fresh, original.ID).Error)
	require.EqualValues(t, model.TaskStatusFailure, fresh.Status)
	require.Equal(t, original.FinishTime, fresh.FinishTime)
	require.Equal(t, original.Quota, fresh.Quota)
	require.Equal(t, original.PrivateData, fresh.PrivateData)
	require.Equal(t, original.Data, fresh.Data)
	return fresh
}

func TestVideoFeeLateFailureReasonEnrichmentAndConcurrentReplay(t *testing.T) {
	task, event := lateFailureFixture(t)
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- processVideoFeeWebhook(context.Background(), event) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	fresh := assertLateFailureNoFinancialEffects(t, task)
	require.Contains(t, fresh.FailReason, "InputImageSensitiveContentDetected")
	public := PublicTaskFailure(&fresh)
	require.Contains(t, public, "InputImageSensitiveContentDetected")
	require.NotContains(t, public, "private-secret")
	require.NoError(t, processVideoFeeWebhook(context.Background(), event))
	after := assertLateFailureNoFinancialEffects(t, task)
	require.Equal(t, fresh.FailReason, after.FailReason)
	require.Equal(t, fresh.UpdatedAt, after.UpdatedAt)
}

func TestVideoFeeLateFailureDoesNotOverwriteDetailOrSuccess(t *testing.T) {
	for _, status := range []model.TaskStatus{model.TaskStatusFailure, model.TaskStatusSuccess} {
		t.Run(string(status), func(t *testing.T) {
			task, event := lateFailureFixture(t)
			require.NoError(t, model.DB.Model(task).Updates(map[string]any{"status": status, "fail_reason": "Specific original failure"}).Error)
			require.NoError(t, processVideoFeeWebhook(context.Background(), event))
			var fresh model.Task
			require.NoError(t, model.DB.First(&fresh, task.ID).Error)
			require.Equal(t, status, fresh.Status)
			require.Equal(t, "Specific original failure", fresh.FailReason)
		})
	}
}

func TestVideoFeeLateFailureRejectsMismatchedTaskAndGenericPayload(t *testing.T) {
	task, event := lateFailureFixture(t)
	event.Payload = `{"data":{"task_id":"another-task","status":"FAILURE","fail_reason":"Specific wrong task failure"}}`
	require.ErrorIs(t, processVideoFeeWebhook(context.Background(), event), ErrVideoFeeWebhookPayload)
	event.Payload = `{"data":{"task_id":"supplier-late-error","status":"FAILURE","fail_reason":"Video generation failed"}}`
	require.NoError(t, processVideoFeeWebhook(context.Background(), event))
	event.Payload = `{"data":{"task_id":"supplier-late-error","status":"SUCCESS","fail_reason":"Contradictory event"}}`
	require.NoError(t, processVideoFeeWebhook(context.Background(), event))
	fresh := assertLateFailureNoFinancialEffects(t, task)
	require.Equal(t, task.FailReason, fresh.FailReason)
	// error.message is an accepted alternate provider reason.
	event.Payload = `{"data":{"task_id":"supplier-late-error","status":"FAILED","error":{"message":"InputImageSensitiveContentDetected"}}}`
	require.NoError(t, processVideoFeeWebhook(context.Background(), event))
	fresh = assertLateFailureNoFinancialEffects(t, task)
	require.Equal(t, "InputImageSensitiveContentDetected", fresh.FailReason)
}

type lateFailureRaceAdaptor struct {
	mockAdaptor
	taskID int64
}

func (a *lateFailureRaceAdaptor) ParseTaskResult([]byte) (*relaycommon.TaskInfo, error) {
	// Polling wins while the callback has an old nonterminal snapshot.
	err := model.DB.Model(&model.Task{}).Where("id = ?", a.taskID).Updates(map[string]any{"status": model.TaskStatusFailure, "fail_reason": "Video generation failed", "finish_time": 100, "progress": "100%"}).Error
	return &relaycommon.TaskInfo{TaskID: "supplier-late-error", Status: string(model.TaskStatusFailure), Reason: "InputImageSensitiveContentDetected", Progress: "100%"}, err
}
func TestVideoFeeLateFailureEnrichesPollingWinnerWithoutRefund(t *testing.T) {
	task, event := lateFailureFixture(t)
	require.NoError(t, model.DB.Model(task).Updates(map[string]any{"status": model.TaskStatusInProgress, "quota": 100}).Error)
	old := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return &lateFailureRaceAdaptor{taskID: task.ID} }
	t.Cleanup(func() { GetTaskAdaptorFunc = old })
	require.NoError(t, processVideoFeeWebhook(context.Background(), event))
	task.Quota = 100
	fresh := assertLateFailureNoFinancialEffects(t, task)
	require.Contains(t, fresh.FailReason, "InputImageSensitiveContentDetected")
}
