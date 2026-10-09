package model

import (
	"errors"
	"github.com/QuantumNous/new-api/constant"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func webhookTestDB(t *testing.T) *gorm.DB {
	old := DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sql, _ := db.DB()
	sql.SetMaxOpenConns(1)
	DB = db
	t.Cleanup(func() { DB = old; sql.Close() })
	require.NoError(t, db.AutoMigrate(&Task{}, &TaskWebhookEvent{}, &VideoVerificationRun{}))
	return db
}
func TestTaskWebhookSeedanceAtomic(t *testing.T) {
	for _, name := range []string{"seedance-2.0", "seedance-2.5", "seedance-2.0-fast", "seedance-2.0-mini"} {
		t.Run(name, func(t *testing.T) {
			db := webhookTestDB(t)
			task := Task{TaskID: GenerateTaskID(), UserId: 1, Status: TaskStatusSubmitted, Properties: Properties{OriginModelName: name}, PrivateData: TaskPrivateData{Webhook: &TaskWebhookConfig{EndpointID: "endpoint"}}}
			require.NoError(t, task.Insert())
			task.Status = TaskStatusSuccess
			won, err := task.UpdateWithStatus(TaskStatusSubmitted)
			require.NoError(t, err)
			require.True(t, won)
			var rows []TaskWebhookEvent
			require.NoError(t, db.Find(&rows).Error)
			require.Len(t, rows, 1)
			require.Contains(t, rows[0].Payload, name)
			won, err = task.UpdateWithStatus(TaskStatusSubmitted)
			require.NoError(t, err)
			require.False(t, won)
			rows = nil
			db.Find(&rows)
			require.Len(t, rows, 1)
		})
	}
}
func TestTaskWebhookRollback(t *testing.T) {
	db := webhookTestDB(t)
	task := Task{TaskID: GenerateTaskID(), Status: TaskStatusSubmitted, PrivateData: TaskPrivateData{Webhook: &TaskWebhookConfig{EndpointID: "endpoint"}}}
	require.NoError(t, task.Insert())
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("fail_event", func(tx *gorm.DB) {
		if tx.Statement.Table == "task_webhook_events" {
			tx.AddError(errors.New("outbox unavailable"))
		}
	}))
	task.Status = TaskStatusFailure
	_, err := task.UpdateWithStatus(TaskStatusSubmitted)
	require.Error(t, err)
	var fresh Task
	require.NoError(t, db.First(&fresh, task.ID).Error)
	require.Equal(t, TaskStatus(TaskStatusSubmitted), fresh.Status)
}

func TestTaskWebhookVideoFailureNotifiesOnce(t *testing.T) {
	db := webhookTestDB(t)
	task := Task{TaskID: GenerateTaskID(), UserId: 1, Platform: constant.TaskPlatformApimartVideo,
		Status: TaskStatusSubmitted, Properties: Properties{OriginModelName: "seedance-2.0-mini"},
		PrivateData: TaskPrivateData{Webhook: &TaskWebhookConfig{EndpointID: "endpoint"}}}
	require.NoError(t, task.Insert())
	task.Status = TaskStatusFailure
	task.FailReason = "content safety review failed; Bearer private-secret"
	won, err := task.UpdateWithStatus(TaskStatusSubmitted)
	require.NoError(t, err)
	require.True(t, won)
	var rows []TaskWebhookEvent
	require.NoError(t, db.Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Contains(t, rows[0].Payload, `"type":"task.failed"`)
	require.Contains(t, rows[0].Payload, `"status":"failed"`)
	require.Contains(t, rows[0].Payload, "/v1/videos/"+task.TaskID)
	require.NotContains(t, rows[0].Payload, "private-secret")
	won, err = task.UpdateWithStatus(TaskStatusSubmitted)
	require.NoError(t, err)
	require.False(t, won)
	var count int64
	require.NoError(t, db.Model(&TaskWebhookEvent{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestTaskWebhookSeedance20LastFrame(t *testing.T) {
	for _, requested := range []bool{true, false} {
		db := webhookTestDB(t)
		task := Task{TaskID: GenerateTaskID(), UserId: 1, Status: TaskStatusSuccess, Properties: Properties{OriginModelName: "seedance-2.0"}, PrivateData: TaskPrivateData{SeedanceRequest: map[string]any{"return_last_frame": requested}, Webhook: &TaskWebhookConfig{EndpointID: "endpoint"}}, Data: []byte(`{"data":{"result":{"last_frame_url":"https://private.example/frame.jpg"}}}`)}
		require.NoError(t, task.Insert())
		var event TaskWebhookEvent
		require.NoError(t, db.First(&event).Error)
		if requested {
			require.Contains(t, event.Payload, "/v1/videos/"+task.TaskID+"/last-frame")
		} else {
			require.NotContains(t, event.Payload, "/last-frame")
		}
		require.NotContains(t, event.Payload, "private.example")
	}
}
func TestTaskWebhookCompletedInsert(t *testing.T) {
	db := webhookTestDB(t)
	task := Task{TaskID: GenerateTaskID(), Platform: constant.TaskPlatformOpenAIImage, Status: TaskStatusSuccess, PrivateData: TaskPrivateData{ImageResultURLs: []string{"https://provider/1", "https://provider/2"}, Webhook: &TaskWebhookConfig{EndpointID: "ep"}}}
	require.NoError(t, task.Insert())
	var e TaskWebhookEvent
	require.NoError(t, db.First(&e).Error)
	require.Contains(t, e.Payload, "/outputs/0/content")
	require.Contains(t, e.Payload, "/outputs/1/content")
	require.NotContains(t, e.Payload, "provider")
}
func TestTaskWebhookBulkFailure(t *testing.T) {
	db := webhookTestDB(t)
	a := Task{TaskID: GenerateTaskID(), Status: TaskStatusSubmitted, PrivateData: TaskPrivateData{Webhook: &TaskWebhookConfig{EndpointID: "ep"}}}
	b := Task{TaskID: GenerateTaskID(), Status: TaskStatusSuccess}
	require.NoError(t, a.Insert())
	require.NoError(t, b.Insert())
	require.NoError(t, TaskBulkUpdateByID([]int64{a.ID, b.ID}, map[string]any{"status": "FAILURE", "progress": "100%"}))
	var fresh Task
	require.NoError(t, db.First(&fresh, b.ID).Error)
	require.Equal(t, TaskStatus(TaskStatusSuccess), fresh.Status)
	var count int64
	db.Model(&TaskWebhookEvent{}).Count(&count)
	require.EqualValues(t, 1, count)
}
