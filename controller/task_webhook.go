package controller

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func taskWebhookError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"error": gin.H{"message": message}})
}
func GetTaskWebhookConfig(c *gin.Context) *model.TaskWebhookConfig {
	value, ok := c.Get(service.TaskWebhookContextKey)
	if !ok {
		return nil
	}
	config, _ := value.(*model.TaskWebhookConfig)
	return config
}
func CreateTaskWebhookEndpoint(c *gin.Context) {
	var body struct {
		URL string `json:"url"`
	}
	if c.ShouldBindJSON(&body) != nil {
		taskWebhookError(c, 400, "url is required")
		return
	}
	if _, err := service.ValidateTaskWebhookURL(body.URL); err != nil {
		taskWebhookError(c, 400, err.Error())
		return
	}
	var count int64
	model.DB.Model(&model.TaskWebhookEndpoint{}).Where("user_id = ?", c.GetInt("id")).Count(&count)
	if count >= 20 {
		taskWebhookError(c, 429, "At most 20 webhook endpoints per account")
		return
	}
	secret, err := common.GenerateRandomCharsKey(48)
	if err != nil {
		taskWebhookError(c, 503, "Secret generation unavailable")
		return
	}
	encrypted, err := service.EncryptTaskWebhookSecret(secret)
	if err != nil {
		taskWebhookError(c, 503, "Webhook signing configuration unavailable")
		return
	}
	e := model.TaskWebhookEndpoint{ID: model.NewWebhookID("whep_"), UserID: c.GetInt("id"), URL: body.URL, Secret: encrypted, KeyID: model.NewWebhookID("key_"), Enabled: true, CreatedAt: time.Now().Unix()}
	if model.DB.Create(&e).Error != nil {
		taskWebhookError(c, 503, "Unable to save endpoint")
		return
	}
	c.JSON(201, gin.H{"endpoint": e, "signing_secret": secret})
}
func ListTaskWebhookEndpoints(c *gin.Context) {
	var rows []model.TaskWebhookEndpoint
	if err := model.DB.Where("user_id = ?", c.GetInt("id")).Order("created_at desc").Limit(20).Find(&rows).Error; err != nil {
		taskWebhookError(c, 503, "Endpoint lookup unavailable")
		return
	}
	c.JSON(200, gin.H{"data": rows})
}
func VerifyTaskWebhookEndpoint(c *gin.Context) {
	e, err := model.WebhookEndpointForUser(c.Param("endpoint_id"), c.GetInt("id"))
	if err != nil {
		taskWebhookError(c, 404, "Endpoint not found")
		return
	}
	if err = service.VerifyTaskWebhookEndpoint(c.Request.Context(), e); err != nil {
		taskWebhookError(c, 400, err.Error())
		return
	}
	if err = model.DB.Model(e).Update("verified", true).Error; err != nil {
		taskWebhookError(c, 503, "Unable to persist verification")
		return
	}
	e.Verified = true
	c.JSON(200, e)
}
func UpdateTaskWebhookEndpoint(c *gin.Context) {
	e, err := model.WebhookEndpointForUser(c.Param("endpoint_id"), c.GetInt("id"))
	if err != nil {
		taskWebhookError(c, 404, "Endpoint not found")
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if c.ShouldBindJSON(&body) != nil || body.Enabled == nil {
		taskWebhookError(c, 400, "enabled boolean is required; create a new endpoint to change URL")
		return
	}
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(e).Update("enabled", *body.Enabled).Error; err != nil {
			return err
		}
		if *body.Enabled && e.Verified {
			return tx.Model(&model.TaskWebhookEvent{}).Where("endpoint_id = ? AND user_id = ? AND status = ? AND deadline > ?", e.ID, e.UserID, "paused", time.Now().Unix()).Updates(map[string]any{"status": "retrying", "next_at": time.Now().Unix()}).Error
		}
		return nil
	})
	if err != nil {
		taskWebhookError(c, 503, "Unable to update endpoint")
		return
	}
	e.Enabled = *body.Enabled
	c.JSON(200, e)
}
func RotateTaskWebhookSecret(c *gin.Context) {
	e, err := model.WebhookEndpointForUser(c.Param("endpoint_id"), c.GetInt("id"))
	if err != nil {
		taskWebhookError(c, 404, "Endpoint not found")
		return
	}
	secret, err := common.GenerateRandomCharsKey(48)
	if err != nil {
		taskWebhookError(c, 503, "Secret generation unavailable")
		return
	}
	encrypted, err := service.EncryptTaskWebhookSecret(secret)
	if err != nil {
		taskWebhookError(c, 503, "Signing configuration unavailable")
		return
	}
	previousKeyID := e.KeyID
	keyID := model.NewWebhookID("key_")
	until := time.Now().Unix() + 86400
	res := model.DB.Model(e).Where("key_id = ?", e.KeyID).Updates(map[string]any{"secret": encrypted, "key_id": keyID, "previous_secret": e.Secret, "previous_key_id": e.KeyID, "previous_until": until})
	if res.Error != nil || res.RowsAffected != 1 {
		taskWebhookError(c, 409, "Endpoint changed; retry lookup before rotating")
		return
	}
	c.JSON(200, gin.H{"endpoint_id": e.ID, "key_id": keyID, "signing_secret": secret, "previous_key_id": previousKeyID, "previous_valid_until": until})
}
func taskWebhookPage(c *gin.Context) (int, int) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	offset, _ := strconv.Atoi(c.Query("offset"))
	if offset < 0 || offset > 10000 {
		offset = 0
	}
	return limit, offset
}
func ListTaskWebhookEvents(c *gin.Context) {
	q := model.DB.Where("user_id = ?", c.GetInt("id"))
	if id := c.Query("task_id"); id != "" {
		q = q.Where("task_id = ?", id)
	}
	if status := c.Query("status"); status != "" {
		q = q.Where("status = ?", status)
	}
	limit, offset := taskWebhookPage(c)
	var events []model.TaskWebhookEvent
	if q.Order("created_at desc, id").Limit(limit).Offset(offset).Find(&events).Error != nil {
		taskWebhookError(c, 503, "Event lookup unavailable")
		return
	}
	c.JSON(200, gin.H{"data": events})
}
func GetTaskWebhookEvent(c *gin.Context) {
	var e model.TaskWebhookEvent
	if model.DB.Where("id = ? AND user_id = ?", c.Param("event_id"), c.GetInt("id")).First(&e).Error != nil {
		taskWebhookError(c, 404, "Event not found")
		return
	}
	var payload any
	_ = common.UnmarshalJsonStr(e.Payload, &payload)
	limit, offset := taskWebhookPage(c)
	var attempts []model.TaskWebhookAttempt
	if model.DB.Where("event_id = ?", e.ID).Order("started_at desc").Limit(limit).Offset(offset).Find(&attempts).Error != nil {
		taskWebhookError(c, 503, "Delivery lookup unavailable")
		return
	}
	c.JSON(200, gin.H{"event": e, "payload": payload, "deliveries": attempts})
}
func RedeliverTaskWebhookEvent(c *gin.Context) {
	now := time.Now().Unix()
	var e model.TaskWebhookEvent
	if model.DB.Where("id = ? AND user_id = ?", c.Param("event_id"), c.GetInt("id")).First(&e).Error != nil {
		taskWebhookError(c, 404, "Event not found")
		return
	}
	if e.CreatedAt < now-30*86400 {
		taskWebhookError(c, 410, "Event retention expired")
		return
	}
	endpoint, err := model.WebhookEndpointForUser(e.EndpointID, e.UserID)
	if err != nil || !endpoint.Enabled || !endpoint.Verified {
		taskWebhookError(c, 400, "Endpoint must be verified and enabled")
		return
	}
	day := now / 86400
	count := e.ReplayCount
	if e.ReplayDay != day {
		count = 0
	}
	if count >= 5 {
		taskWebhookError(c, 429, "At most five replays per event per day")
		return
	}
	res := model.DB.Model(&e).Where("lease_until < ? AND replay_day = ? AND replay_count = ?", now, e.ReplayDay, e.ReplayCount).Updates(map[string]any{"status": "pending", "next_at": now, "deadline": now + 72*3600, "lease": "", "lease_until": 0, "replay_day": day, "replay_count": count + 1})
	if res.Error != nil {
		taskWebhookError(c, 503, "Unable to queue replay")
		return
	}
	if res.RowsAffected != 1 {
		taskWebhookError(c, 409, "Event is being delivered or changed; retry later")
		return
	}
	c.JSON(202, gin.H{"event_id": e.ID, "status": "pending"})
}
func TaskWebhookCapabilities(c *gin.Context) {
	name := c.Query("model")
	c.JSON(200, gin.H{"model": name, "supported": service.TaskWebhookModelSupported(name), "events": []string{"task.completed", "task.failed"}, "api_version": "2026-10-03"})
}

func TaskWebhookOutput(c *gin.Context) {
	id := c.Param("task_id")
	index, err := strconv.Atoi(c.Param("output_id"))
	if err != nil || index < 0 {
		taskWebhookError(c, 404, "Output not found")
		return
	}
	source := ""
	if strings.HasPrefix(id, "imagine_") {
		task, _, err := model.GetImagineTask(id, c.GetInt("id"))
		if err != nil || task.Status != "completed" {
			taskWebhookError(c, 404, "Output not found")
			return
		}
		var urls []string
		_ = common.UnmarshalJsonStr(task.Images, &urls)
		if index < len(urls) {
			source = urls[index]
		}
	} else {
		task, exists, err := model.GetByTaskId(c.GetInt("id"), id)
		if err != nil || !exists || task.Status != model.TaskStatusSuccess {
			taskWebhookError(c, 404, "Output not found")
			return
		}
		urls := task.PrivateData.ImageResultURLs
		if len(urls) == 0 && task.GetResultURL() != "" {
			urls = []string{task.GetResultURL()}
		}
		if index < len(urls) {
			source = urls[index]
		}
	}
	if source == "" {
		taskWebhookError(c, 404, "Output not found")
		return
	}
	// Download validation and SSRF policy are shared with the existing media cache.
	resp, err := service.DoDownloadRequest(source, "task_webhook_output")
	if err != nil {
		taskWebhookError(c, 410, "Media has expired or is no longer available")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		taskWebhookError(c, 410, "Media has expired or is no longer available")
		return
	}
	c.Header("Content-Type", resp.Header.Get("Content-Type"))
	c.Header("Cache-Control", "private, no-store")
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, resp.Body)
}
