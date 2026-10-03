package relay

import (
	"errors"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http/httptest"
	"testing"
)

func TestTaskWebhookNativeGeminiImageResponse(t *testing.T) {
	old := model.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	t.Cleanup(func() { model.DB = old; s, _ := db.DB(); s.Close() })
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.TaskWebhookEvent{}))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(service.TaskWebhookContextKey, &model.TaskWebhookConfig{EndpointID: "ep", Reference: "gemini-order"})
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 1}, UserId: 7, OriginModelName: "gemini-3.1-flash-image-preview"}
	// Cached URLs avoid network dependencies while preserving the adapter's synchronous shape.
	raw := []byte(`{"data":[{"url":"https://apimaster.ai/imgs/one.png"},{"url":"https://apimaster.ai/imgs/two.png"}]}`)
	body, err := persistTaskWebhookImageResponse(c, info, raw)
	require.NoError(t, err)
	require.Contains(t, string(body), "task_id")
	var events []model.TaskWebhookEvent
	require.NoError(t, db.Find(&events).Error)
	require.Len(t, events, 1)
	require.Contains(t, events[0].Payload, "gemini-order")
	require.Contains(t, events[0].Payload, "?model=gemini-3.1-flash-image-preview")
	require.Contains(t, events[0].Payload, "/outputs/1/content")
	// An already tracked OpenAI response keeps its task and creates no duplicate event.
	unchanged, err := persistTaskWebhookImageResponse(c, info, body)
	require.NoError(t, err)
	require.Equal(t, body, unchanged)
	var count int64
	db.Model(&model.TaskWebhookEvent{}).Count(&count)
	require.EqualValues(t, 1, count)
	// Outbox write failure must roll back the completed task and withhold success.
	c.Set("image_poll_task_id", "")
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("fail_webhook", func(tx *gorm.DB) {
		if tx.Statement.Table == "task_webhook_events" {
			tx.AddError(errors.New("outbox unavailable"))
		}
	}))
	_, err = persistTaskWebhookImageResponse(c, info, raw)
	require.Error(t, err)
	db.Model(&model.Task{}).Count(&count)
	require.EqualValues(t, 1, count)
}
