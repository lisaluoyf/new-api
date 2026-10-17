package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

type bannedFallbackAdaptor struct {
	mockAdaptor
	result relaycommon.TaskInfo
}

func (a *bannedFallbackAdaptor) FetchTask(string, string, map[string]any, string) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"status":"test"}`))}, nil
}
func (a *bannedFallbackAdaptor) ParseTaskResult([]byte) (*relaycommon.TaskInfo, error) {
	result := a.result
	return &result, nil
}

func TestSeedanceBannedFallbackPollingAndWebhook(t *testing.T) {
	for _, webhook := range []bool{false, true} {
		for _, backupOK := range []bool{false, true} {
			name := "poll"
			if webhook {
				name = "webhook"
			}
			if backupOK {
				name += "/backup-success"
			} else {
				name += "/backup-failure"
			}
			t.Run(name, func(t *testing.T) {
				t.Setenv("SEEDANCE_AUTO_VERIFIED_MODELS", "seedance-2.0-fast")
				db, task := fallbackFixture(t)
				require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 7).Update("status", common.ChannelStatusManuallyDisabled).Error)
				base := "https://seedance2026.vip"
				primary := &model.Channel{Id: constant.VideoFeeSeedanceChannelID, Type: constant.ChannelTypeOpenAI, Name: "original", Key: "original-key", BaseURL: &base, Status: common.ChannelStatusEnabled}
				require.NoError(t, db.Create(primary).Error)
				model.InitChannelCache()
				task.ChannelId = primary.Id
				task.Platform = constant.TaskPlatformVideoFee
				task.Quota = 500
				task.PrivateData.BillingSource = BillingSourceWallet
				task.PrivateData.BillingContext = &model.TaskBillingContext{PerCallBilling: true}
				require.NoError(t, db.Save(task).Error)
				require.NoError(t, db.Create(&model.User{Id: 1, Username: "fallback", Quota: 1000, UsedQuota: 500}).Error)
				snapshot := `{"channel_id":273,"prices":{"channel_cost":{"input_price":0.1},"channel_user":{"input_price":0.1}},"amounts_usd":{"channel_cost":0.1,"user_final":0.001},"units":{"duration_seconds":5}}`
				require.NoError(t, db.Create(&model.Log{UserId: 1, Type: model.LogTypeConsume, ModelName: task.Properties.OriginModelName, ChannelId: primary.Id, Quota: 500, AccountingStatus: "ok", AccountingSnapshot: snapshot, Other: common.MapToJsonStr(map[string]any{"is_task": true, "task_id": task.TaskID})}).Error)
				failed := &bannedFallbackAdaptor{result: relaycommon.TaskInfo{TaskID: task.GetUpstreamTaskID(), Status: string(model.TaskStatusFailure), Reason: "User has been banned"}}
				old := GetTaskAdaptorFunc
				GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return failed }
				t.Cleanup(func() { GetTaskAdaptorFunc = old })
				ctx := context.Background()
				if webhook {
					event := &model.VideoFeeWebhookInbox{TaskID: task.ID, Payload: `{"status":"FAILURE"}`}
					require.NoError(t, processVideoFeeWebhook(ctx, event))
					require.NoError(t, processVideoFeeWebhook(ctx, event))
				} else {
					require.NoError(t, updateVideoSingleTask(ctx, failed, primary, task.GetUpstreamTaskID(), map[string]*model.Task{task.GetUpstreamTaskID(): task}))
				}
				var fresh model.Task
				require.NoError(t, db.First(&fresh, task.ID).Error)
				require.EqualValues(t, model.TaskStatusRetrying, fresh.Status)
				require.Equal(t, 1000, getUserQuota(t, 1))
				processSeedanceTaskFallbacks(ctx)
				require.NoError(t, db.First(&fresh, task.ID).Error)
				require.Equal(t, task.TaskID, fresh.TaskID)
				require.Equal(t, 8, fresh.ChannelId)
				require.Equal(t, "backup-upstream", fresh.GetUpstreamTaskID())
				if webhook {
					// An already verified primary callback must not complete/refund
					// the public task after it moved to the backup provider.
					require.ErrorIs(t, processVideoFeeWebhook(ctx, &model.VideoFeeWebhookInbox{TaskID: task.ID}), ErrVideoFeeWebhookPayload)
				}
				var backup model.Channel
				require.NoError(t, db.First(&backup, 8).Error)
				result := relaycommon.TaskInfo{TaskID: fresh.GetUpstreamTaskID(), Status: string(model.TaskStatusFailure), Reason: "User has been banned"}
				if backupOK {
					result.Status, result.Url = string(model.TaskStatusSuccess), "https://example.com/result.mp4"
				}
				adaptor := &bannedFallbackAdaptor{result: result}
				// Competing poll responses for the same attempt must settle only once.
				stale := fresh
				for _, copy := range []*model.Task{&fresh, &stale} {
					require.NoError(t, updateVideoSingleTask(ctx, adaptor, &backup, copy.GetUpstreamTaskID(), map[string]*model.Task{copy.GetUpstreamTaskID(): copy}))
				}
				require.NoError(t, db.First(&fresh, task.ID).Error)
				var count int64
				require.NoError(t, db.Model(&model.SeedanceTaskFallback{}).Count(&count).Error)
				require.EqualValues(t, 1, count)
				var user model.User
				require.NoError(t, db.First(&user, 1).Error)
				require.NoError(t, db.Model(&model.Log{}).Where("type = ?", model.LogTypeRefund).Count(&count).Error)
				if backupOK {
					require.EqualValues(t, model.TaskStatusSuccess, fresh.Status)
					require.Equal(t, 1000, user.Quota)
					require.EqualValues(t, 0, count)
				} else {
					require.EqualValues(t, model.TaskStatusFailure, fresh.Status)
					require.Equal(t, 1500, user.Quota)
					require.EqualValues(t, 1, count)
				}
				require.NoError(t, db.Model(&model.VideoVerificationRun{}).Count(&count).Error)
				if backupOK {
					require.EqualValues(t, 1, count)
				} else {
					require.Zero(t, count)
				}
				require.NoError(t, db.Model(&model.Log{}).Where("type = ?", model.LogTypeConsume).Count(&count).Error)
				require.EqualValues(t, 1, count)
			})
		}
	}
}

func TestSeedanceBannedFallbackDoesNotReplayTerminalTasks(t *testing.T) {
	for _, status := range []model.TaskStatus{model.TaskStatusSuccess, model.TaskStatusFailure, model.TaskStatusRetrying} {
		t.Run(string(status), func(t *testing.T) {
			db, task := fallbackFixture(t)
			task.Status, task.FinishTime = status, time.Now().Unix()
			require.NoError(t, db.Save(task).Error)
			started, err := beginSeedanceTaskFallback(context.Background(), task, status, "User has been banned")
			require.NoError(t, err)
			require.False(t, started)
			var count int64
			require.NoError(t, db.Model(&model.SeedanceTaskFallback{}).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestSeedanceBannedFallbackRequiresCompatibleAvailableChannel(t *testing.T) {
	db, task := fallbackFixture(t)
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 8).Update("status", common.ChannelStatusManuallyDisabled).Error)
	model.InitChannelCache()
	started, err := beginSeedanceTaskFallback(context.Background(), task, task.Status, "User has been banned")
	require.NoError(t, err)
	require.False(t, started)
	var fresh model.Task
	require.NoError(t, db.First(&fresh, task.ID).Error)
	require.EqualValues(t, model.TaskStatusSubmitted, fresh.Status)
	var count int64
	require.NoError(t, db.Model(&model.SeedanceTaskFallback{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestSeedanceBannedFallbackRejectsStaleChannelObservation(t *testing.T) {
	db, task := fallbackFixture(t)
	// Status alone is insufficient: another worker may already have moved the
	// same public task onto a different channel with the same SUBMITTED status.
	require.NoError(t, db.Model(&model.Task{}).Where("id = ?", task.ID).Update("channel_id", 8).Error)
	started, err := beginSeedanceTaskFallback(context.Background(), task, task.Status, "User has been banned")
	require.ErrorIs(t, err, model.ErrVideoSubmissionClaimLost)
	require.False(t, started)
	var fresh model.Task
	require.NoError(t, db.First(&fresh, task.ID).Error)
	require.Equal(t, 8, fresh.ChannelId)
	require.False(t, fresh.PrivateData.SeedanceFallback.Started)
	var count int64
	require.NoError(t, db.Model(&model.SeedanceTaskFallback{}).Count(&count).Error)
	require.Zero(t, count)
}
