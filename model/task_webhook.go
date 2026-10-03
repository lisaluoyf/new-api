package model

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/tidwall/gjson"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TaskWebhookConfig is private gateway configuration, never sent upstream.
type TaskWebhookConfig struct {
	EndpointID string   `json:"endpoint_id"`
	Reference  string   `json:"client_reference_id,omitempty"`
	Events     []string `json:"events,omitempty"`
}

type TaskWebhookEndpoint struct {
	ID             string `json:"id" gorm:"primaryKey;size:64"`
	UserID         int    `json:"-" gorm:"index"`
	URL            string `json:"url" gorm:"type:text"`
	Secret         string `json:"-" gorm:"type:text"`
	KeyID          string `json:"key_id" gorm:"size:64"`
	PreviousSecret string `json:"-" gorm:"type:text"`
	PreviousKeyID  string `json:"previous_key_id,omitempty" gorm:"size:64"`
	PreviousUntil  int64  `json:"previous_valid_until,omitempty"`
	Verified       bool   `json:"verified"`
	Enabled        bool   `json:"enabled"`
	CreatedAt      int64  `json:"created_at"`
}

type TaskWebhookEvent struct {
	ID          string `json:"id" gorm:"primaryKey;size:64"`
	Resource    string `json:"-" gorm:"uniqueIndex:idx_task_webhook_resource;size:191"`
	UserID      int    `json:"-" gorm:"index:idx_task_webhook_user_time,priority:1"`
	TaskID      string `json:"task_id" gorm:"index;size:191"`
	EndpointID  string `json:"endpoint_id" gorm:"index;size:64"`
	Type        string `json:"type" gorm:"size:32"`
	Payload     string `json:"-" gorm:"type:text"`
	Status      string `json:"delivery_status" gorm:"index:idx_task_webhook_due,priority:1;size:32"`
	Attempts    int    `json:"attempts"`
	NextAt      int64  `json:"next_attempt_at" gorm:"index:idx_task_webhook_due,priority:2"`
	Deadline    int64  `json:"retry_deadline"`
	Lease       string `json:"-" gorm:"size:64"`
	LeaseUntil  int64  `json:"-" gorm:"index"`
	LastHTTP    int    `json:"last_http_status"`
	LastError   string `json:"last_error,omitempty" gorm:"size:255"`
	DeliveredAt int64  `json:"delivered_at,omitempty"`
	ReplayDay   int64  `json:"-"`
	ReplayCount int    `json:"-"`
	CreatedAt   int64  `json:"created_at" gorm:"index:idx_task_webhook_user_time,priority:2"`
}

type TaskWebhookAttempt struct {
	ID         string `json:"id" gorm:"primaryKey;size:64"`
	EventID    string `json:"event_id" gorm:"index;size:64"`
	StartedAt  int64  `json:"started_at"`
	DurationMS int64  `json:"duration_ms"`
	HTTPStatus int    `json:"http_status"`
	Error      string `json:"error,omitempty" gorm:"size:255"`
}

func NewWebhookID(prefix string) string {
	return prefix + strings.TrimPrefix(GenerateTaskID(), "task_")
}
func WebhookEndpointForUser(id string, user int) (*TaskWebhookEndpoint, error) {
	var e TaskWebhookEndpoint
	err := DB.Where("id = ? AND user_id = ?", id, user).First(&e).Error
	return &e, err
}
func webhookOrigin() string { return "https://apimaster.ai" }

func enqueueTaskWebhook(tx *gorm.DB, config *TaskWebhookConfig, resource, taskID, modelName, kind, status string, user int, completed int64, outputs []map[string]any, extra map[string]any) error {
	if config == nil || config.EndpointID == "" {
		return nil
	}
	eventType := "task." + status
	if kind == "batch" {
		eventType = "batch.completed"
	}
	if len(config.Events) > 0 {
		found := false
		for _, v := range config.Events {
			if v == eventType {
				found = true
			}
		}
		if !found {
			return nil
		}
	}
	now := time.Now().Unix()
	if completed == 0 {
		completed = now
	}
	var prior TaskWebhookEvent
	if err := tx.Where("resource = ?", resource).First(&prior).Error; err == nil {
		return nil
	} else if err != gorm.ErrRecordNotFound {
		return err
	}
	id := NewWebhookID("evt_")
	taskURL := webhookOrigin() + "/v1/tasks/" + taskID
	if kind == "video" {
		taskURL = webhookOrigin() + "/v1/videos/" + taskID
	}
	data := map[string]any{"task_id": taskID, "model": modelName, "kind": kind, "status": status, "client_reference_id": config.Reference, "completed_at": completed, "task_url": taskURL, "result": nil, "error": nil}
	if status == "completed" {
		data["result"] = map[string]any{"outputs": outputs}
	} else if kind != "batch" {
		data["error"] = map[string]string{"code": "generation_failed", "message": "The generation task failed. Query the task for details."}
	}
	for k, v := range extra {
		data[k] = v
	}
	body, err := common.Marshal(map[string]any{"id": id, "type": eventType, "api_version": "2026-10-03", "created_at": now, "data": data})
	if err != nil {
		return err
	}
	e := TaskWebhookEvent{ID: id, Resource: resource, UserID: user, TaskID: taskID, EndpointID: config.EndpointID, Type: eventType, Payload: string(body), Status: "pending", NextAt: now, Deadline: now + 72*3600, CreatedAt: now}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&e).Error
}

func (t *Task) AfterCreate(tx *gorm.DB) error { return t.enqueueWebhook(tx) }
func (t *Task) enqueueWebhook(tx *gorm.DB) error {
	if t.PrivateData.Webhook == nil || (t.Status != TaskStatusSuccess && t.Status != TaskStatusFailure) {
		return nil
	}
	kind := "video"
	if t.Platform == constant.TaskPlatformOpenAIImage {
		kind = "image"
	} else if t.Platform == constant.TaskPlatformSuno {
		kind = "audio"
		if t.Action == constant.SunoActionLyrics {
			kind = "text"
		}
	}
	status := "failed"
	if t.Status == TaskStatusSuccess {
		status = "completed"
	}
	outputs := []map[string]any{}
	if status == "completed" {
		if kind == "video" {
			outputs = append(outputs, map[string]any{"output_id": "0", "type": "video", "url": webhookOrigin() + "/v1/videos/" + t.TaskID + "/content", "authentication": "bearer", "expires_at": nil})
			if requested, _ := t.PrivateData.SeedanceRequest["return_last_frame"].(bool); requested {
				for _, path := range []string{"data.result.videos.0.last_frame_url", "result.videos.0.last_frame_url", "data.content.last_frame_url", "content.last_frame_url", "data.last_frame_url", "last_frame_url"} {
					if gjson.GetBytes(t.Data, path).String() != "" {
						outputs = append(outputs, map[string]any{"output_id": "last_frame", "type": "image", "url": webhookOrigin() + "/v1/videos/" + t.TaskID + "/last-frame", "authentication": "bearer", "expires_at": nil})
						break
					}
				}
			}
		} else {
			urls := t.PrivateData.ImageResultURLs
			if len(urls) == 0 && t.GetResultURL() != "" {
				urls = []string{t.GetResultURL()}
			}
			for i := range urls {
				outputs = append(outputs, map[string]any{"output_id": strconv.Itoa(i), "type": kind, "url": webhookOrigin() + "/v1/tasks/" + t.TaskID + "/outputs/" + strconv.Itoa(i) + "/content", "authentication": "bearer", "expires_at": nil})
			}
		}
	}
	return enqueueTaskWebhook(tx, t.PrivateData.Webhook, fmt.Sprintf("task:%d:terminal", t.ID), t.TaskID, t.Properties.OriginModelName, kind, status, t.UserId, t.FinishTime, outputs, nil)
}

func EnqueueImagineWebhook(tx *gorm.DB, b *ImagineBatch, t *ImagineTask) error {
	if b.Webhook == nil {
		return nil
	}
	var urls []string
	_ = common.UnmarshalJsonStr(t.Images, &urls)
	outputs := []map[string]any{}
	for i := range urls {
		outputs = append(outputs, map[string]any{"output_id": strconv.Itoa(i), "type": "image", "url": webhookOrigin() + "/v1/tasks/" + t.ID + "/outputs/" + strconv.Itoa(i) + "/content", "authentication": "bearer", "expires_at": t.ExpiresAt})
	}
	if err := enqueueTaskWebhook(tx, b.Webhook, "imagine:"+t.ID+":terminal", t.ID, b.Model, "image", t.Status, b.UserID, t.CompletedAt, outputs, map[string]any{"batch_id": b.ID}); err != nil {
		return err
	}
	if b.Status == "finished" {
		var children []ImagineTask
		if err := tx.Where("batch_id = ?", b.ID).Find(&children).Error; err != nil {
			return err
		}
		succeeded := 0
		for _, child := range children {
			if child.Status == "completed" {
				succeeded++
			}
		}
		status := "completed"
		if succeeded == 0 {
			status = "failed"
		} else if succeeded < len(children) {
			status = "partial_failure"
		}
		return enqueueTaskWebhook(tx, b.Webhook, "imagine-batch:"+b.ID+":terminal", b.ID, b.Model, "batch", status, b.UserID, time.Now().Unix(), nil, map[string]any{"batch_id": b.ID, "summary": map[string]int{"total": len(children), "succeeded": succeeded, "failed": len(children) - succeeded}})
	}
	return nil
}
