package model

import (
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"os"
	"sync"
	"testing"
)

func TestTaskWebhookPostgresTransactionConcurrency(t *testing.T) {
	dsn := os.Getenv("TASK_WEBHOOK_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL DSN not supplied")
	}
	old := DB
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	sql, _ := db.DB()
	t.Cleanup(func() { DB = old; sql.Close() })
	require.NoError(t, db.AutoMigrate(&Task{}, &TaskWebhookEvent{}, &TaskWebhookEndpoint{}, &TaskWebhookAttempt{}, &ImagineBatch{}, &ImagineTask{}))
	task := Task{TaskID: GenerateTaskID(), Status: TaskStatusSubmitted, UserId: 1, PrivateData: TaskPrivateData{Webhook: &TaskWebhookConfig{EndpointID: "ep"}}}
	require.NoError(t, task.Insert())
	var wg sync.WaitGroup
	var mu sync.Mutex
	winners := 0
	errs := []error{}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			copy := task
			copy.Status = TaskStatusSuccess
			won, err := copy.UpdateWithStatus(TaskStatusSubmitted)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
			}
			if won {
				winners++
			}
		}()
	}
	wg.Wait()
	require.Empty(t, errs)
	require.Equal(t, 1, winners)
	var count int64
	db.Model(&TaskWebhookEvent{}).Count(&count)
	require.EqualValues(t, 1, count)
	batch := ImagineBatch{ID: "batch-json", Webhook: &TaskWebhookConfig{EndpointID: "ep", Events: []string{"task.failed"}}}
	require.NoError(t, db.Create(&batch).Error)
	var fresh ImagineBatch
	require.NoError(t, db.First(&fresh, "id = ?", batch.ID).Error)
	require.Equal(t, batch.Webhook, fresh.Webhook)
}
