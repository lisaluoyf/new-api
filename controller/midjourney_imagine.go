package controller

import (
	"bytes"
	"context"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func imagineError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"error": gin.H{"message": common.MessageWithRequestId(message, c.GetString(common.RequestIdKey)), "type": "invalid_request_error", "code": status}})
}

func RelayImagine(c *gin.Context) {
	var raw map[string]any
	if err := common.UnmarshalBodyReusable(c, &raw); err != nil {
		imagineError(c, 400, "Invalid JSON request")
		return
	}
	body, err := common.Marshal(raw)
	if err != nil {
		imagineError(c, 400, "Invalid JSON request")
		return
	}
	request, err := dto.ParseImagineRequest(body)
	if err != nil {
		imagineError(c, 400, err.Error())
		return
	}
	basePrice, ok := ratio_setting.GetVideoModelPrice(request.Model, request.Speed)
	if !ok {
		imagineError(c, 503, "Model speed pricing is not configured")
		return
	}
	callbackBase := service.MediaTaskWebhookBase()
	if callbackBase == "" {
		imagineError(c, 503, "Image task service is not configured")
		return
	}
	info := relaycommon.GenRelayInfoImage(c, nil)
	info.InitChannelMeta(c)
	info.OriginModelName = request.Model
	priceData, err := helper.ModelPriceHelperPerCall(c, info)
	if err != nil {
		imagineError(c, 503, "Model pricing is unavailable")
		return
	}
	userPrice, err := service.ChannelBaseUserPriceResolved(info.ChannelId, request.Model, basePrice)
	if err != nil || userPrice <= 0 {
		imagineError(c, 503, "Model pricing is unavailable")
		return
	}
	unitQuota := int(math.Round(userPrice * common.QuotaPerUnit * priceData.GroupRatioInfo.GroupRatio))
	priceData.ModelPrice = userPrice
	priceData.Quota = unitQuota * request.Repeat
	priceData.QuotaToPreConsume = priceData.Quota
	info.PriceData = priceData
	info.ForcePreConsume = true
	if apiErr := service.PreConsumeBilling(c, priceData.Quota, info); apiErr != nil {
		imagineError(c, apiErr.StatusCode, "Insufficient available quota for this generation")
		return
	}
	secret, err := common.GenerateRandomCharsKey(64)
	if err != nil {
		info.Billing.Refund(c)
		imagineError(c, 500, "Could not initialize image task")
		return
	}
	batch := &model.ImagineBatch{ID: "imagine_batch_" + strings.TrimPrefix(model.GenerateTaskID(), "task_"), RequestID: info.RequestId, CallbackToken: secret,
		UserID: info.UserId, TokenID: info.TokenId, TokenName: c.GetString("token_name"), ChannelID: info.ChannelId, Model: request.Model, Speed: request.Speed, Version: request.Payload["version"].(string), Niji: request.Model == "midjourney-niji-7",
		Size: request.Payload["size"].(string), Repeat: request.Repeat, BaseUnitPrice: basePrice, FinalMultiplier: userPrice / basePrice * priceData.GroupRatioInfo.GroupRatio, UnitQuota: unitQuota,
		ReservedQuota: info.Billing.GetPreConsumedQuota(), BillingSource: info.BillingSource, SubscriptionID: info.SubscriptionId, Group: info.UsingGroup, Status: "submitting", CreatedAt: time.Now().Unix(), RequestData: string(body)}
	if err := model.DB.Create(batch).Error; err != nil {
		info.Billing.Refund(c)
		imagineError(c, 500, "Could not persist image task")
		return
	}
	c.Header("X-Task-ID", batch.ID)
	// The durable batch now owns the reservation, including ambiguous HTTP outcomes.
	if err := info.Billing.Settle(batch.ReservedQuota); err != nil {
		imagineError(c, 500, "Could not initialize task billing")
		return
	}
	request.Payload["webhook"] = callbackBase + "/imagine/" + secret
	payload, err := common.Marshal(request.Payload)
	if err != nil {
		_ = model.RejectImagineSubmission(batch.ID)
		imagineError(c, 400, "Invalid Imagine parameters")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(info.ChannelBaseUrl, "/")+"/v1/midjourney/generations", bytes.NewReader(payload))
	if err != nil {
		_ = model.RejectImagineSubmission(batch.ID)
		imagineError(c, 503, "Image service unavailable")
		return
	}
	req.Header.Set("Authorization", "Bearer "+info.ApiKey)
	req.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 120 * time.Second}).Do(req)
	if err != nil {
		markImagineSubmissionUnknown(batch.ID)
		imagineError(c, 502, "Submission outcome pending verification; do not resubmit automatically")
		return
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		markImagineSubmissionUnknown(batch.ID)
		imagineError(c, 502, "Submission outcome pending verification")
		return
	}
	if err := model.DB.Model(batch).Update("submission_response", string(responseBody)).Error; err != nil {
		markImagineSubmissionUnknown(batch.ID)
		imagineError(c, 503, "Submission outcome pending verification")
		return
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode >= 400 && response.StatusCode < 500 && response.StatusCode != 408 {
			_ = model.RejectImagineSubmission(batch.ID)
		} else {
			markImagineSubmissionUnknown(batch.ID)
		}
		status := response.StatusCode
		if status == 401 || status == 402 || status == 403 {
			status = 503
		}
		imagineError(c, status, "Image service could not accept this request")
		return
	}
	ids := []string{}
	for _, item := range gjson.GetBytes(responseBody, "data").Array() {
		if id := item.Get("task_id").String(); id != "" {
			ids = append(ids, id)
		}
	}
	if err := model.AttachImagineTasks(batch.ID, ids); err != nil {
		markImagineSubmissionUnknown(batch.ID)
		imagineError(c, 502, "Submission outcome pending verification")
		return
	}
	var tasks []model.ImagineTask
	if err := model.DB.Where("batch_id = ?", batch.ID).Find(&tasks).Error; err != nil {
		imagineError(c, 503, "Task storage is temporarily unavailable")
		return
	}
	items := []gin.H{}
	for _, task := range tasks {
		items = append(items, gin.H{"id": task.ID, "task_id": task.ID, "status": task.Status})
	}
	c.JSON(http.StatusOK, gin.H{"id": batch.ID, "request_id": batch.RequestID, "model": batch.Model, "status": "queued", "actual_task_count": len(tasks), "data": items})
}

func markImagineSubmissionUnknown(id string) {
	_ = model.DB.Model(&model.ImagineBatch{}).Where("id = ? AND status = ?", id, "submitting").Update("status", "submission_unknown").Error
}

func ImagineCallback(c *gin.Context) {
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 4<<20))
	if err != nil {
		c.JSON(400, gin.H{"ok": false})
		return
	}
	status, err := service.SaveImagineCallback(c.Param("token"), body)
	c.JSON(status, gin.H{"ok": err == nil})
}

func RelayImagineTask(c *gin.Context) {
	id := c.Param("task_id")
	userID := c.GetInt("id")
	var batch *model.ImagineBatch
	var tasks []model.ImagineTask
	if strings.HasPrefix(id, "imagine_batch_") {
		var err error
		batch, err = model.GetImagineBatch(id)
		if err != nil || batch.UserID != userID {
			imagineError(c, 404, "Task not found")
			return
		}
		if err := model.DB.Where("batch_id = ?", id).Find(&tasks).Error; err != nil {
			imagineError(c, 503, "Task storage unavailable")
			return
		}
	} else {
		task, b, err := model.GetImagineTask(id, userID)
		if err != nil {
			imagineError(c, 404, "Task not found")
			return
		}
		batch = b
		tasks = []model.ImagineTask{*task}
	}
	images := []gin.H{}
	entries := []gin.H{}
	completed, failed := 0, 0
	progress := 0
	for _, task := range tasks {
		if task.Status != "completed" && task.Status != "failed" {
			now := time.Now().Unix()
			// Active queries may refresh every 15s; background polling starts after 3min.
			claim := model.DB.Model(&model.ImagineTask{}).Where("id = ? AND next_poll_at <= ?", task.ID, now+165).Update("next_poll_at", now+180)
			if claim.Error == nil && claim.RowsAffected > 0 {
				_ = service.QueryImagineTask(c.Request.Context(), &task)
			}
			_ = model.DB.Where("id = ?", task.ID).First(&task).Error
		}
		var urls []string
		_ = common.UnmarshalJsonStr(task.Images, &urls)
		data := []gin.H{}
		for _, u := range urls {
			image := gin.H{"url": u}
			data = append(data, image)
			images = append(images, image)
		}
		entry := gin.H{"id": task.ID, "task_id": task.ID, "status": task.Status, "progress": task.Progress, "data": data, "created_at": task.CreatedAt}
		if task.GridURL != "" {
			entry["grid_image_url"] = task.GridURL
		}
		if task.ExpiresAt > 0 {
			entry["expires_at"] = task.ExpiresAt
		}
		if task.Status == "completed" {
			completed++
			entry["completed_at"] = task.CompletedAt
		}
		if task.Status == "failed" {
			failed++
			entry["error"] = gin.H{"message": "Image generation failed", "type": "image_generation_error"}
		}
		progress += task.Progress
		entries = append(entries, entry)
	}
	status := "processing"
	if completed == len(tasks) && len(tasks) > 0 {
		status = "completed"
	}
	if failed == len(tasks) && len(tasks) > 0 {
		status = "failed"
	}
	if completed+failed == len(tasks) && completed > 0 && failed > 0 {
		status = "completed"
	}
	if len(tasks) == 0 {
		status = "queued"
		if batch.Status == "submission_unknown" {
			status = "pending_timeout"
		}
		if batch.Status == "rejected" {
			status = "failed"
		}
	}
	if len(tasks) > 0 {
		progress /= len(tasks)
	}
	c.JSON(200, gin.H{"id": id, "request_id": batch.RequestID, "model": batch.Model, "status": status, "progress": progress, "actual_task_count": len(tasks), "data": images, "tasks": entries})
}

func IsImagineImageRequest(c *gin.Context) bool {
	var request struct {
		Model string `json:"model"`
	}
	return common.UnmarshalBodyReusable(c, &request) == nil && dto.IsMidjourneyImagineModel(request.Model)
}

func ImagineSpeedStats(c *gin.Context) {
	var tasks []struct {
		Speed      string
		ActualTime int64
	}
	err := model.DB.Table("imagine_tasks").Select("imagine_batches.speed, imagine_tasks.actual_time").Joins("JOIN imagine_batches ON imagine_batches.id = imagine_tasks.batch_id").Where("imagine_tasks.status = ? AND imagine_tasks.completed_at >= ?", "completed", time.Now().Add(-30*24*time.Hour).Unix()).Limit(100000).Scan(&tasks).Error
	if err != nil {
		c.JSON(503, gin.H{"success": false})
		return
	}
	groups := map[string][]int64{}
	for _, task := range tasks {
		groups[task.Speed] = append(groups[task.Speed], task.ActualTime)
	}
	result := map[string]any{}
	for speed, times := range groups {
		result[speed] = service.ImagineDurationStats(times)
	}
	c.JSON(200, gin.H{"success": true, "data": result})
}
