package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/tidwall/gjson"
)

func ImagineDurationStats(times []int64) map[string]any {
	if len(times) == 0 {
		return map[string]any{"count": 0}
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	var total int64
	for _, n := range times {
		total += n
	}
	percentile := func(p int) int64 { return times[(len(times)*p+99)/100-1] }
	return map[string]any{"count": len(times), "average": float64(total) / float64(len(times)), "p50": percentile(50), "p90": percentile(90), "p95": percentile(95)}
}

// ProcessImagineResult is shared by callback delivery, active queries and recovery.
func ProcessImagineResult(task *model.ImagineTask, body []byte) error {
	result, err := parseImagineResult(task, body, true)
	if err != nil {
		return err
	}
	result.CompletionSource = "query"
	if task.CallbackPayload != "" {
		result.CompletionSource = "webhook"
	}
	return model.ApplyImagineResult(task.ID, result)
}

func parseImagineResult(task *model.ImagineTask, body []byte, cacheImages bool) (model.ImagineTask, error) {
	if !gjson.ValidBytes(body) {
		return model.ImagineTask{}, fmt.Errorf("invalid task result JSON")
	}
	root := gjson.ParseBytes(body)
	if root.Get("data").IsObject() {
		root = root.Get("data")
	}
	if root.Get("id").String() != task.UpstreamID {
		return model.ImagineTask{}, fmt.Errorf("task result id mismatch")
	}
	status := root.Get("status").String()
	result := model.ImagineTask{OriginalStatus: status, Progress: int(root.Get("progress").Int()), RawResult: root.Raw,
		ProviderCost: root.Get("cost").Float(), CreditsCost: root.Get("credits_cost").Float(), CompletedAt: root.Get("completed").Int(), ActualTime: root.Get("actual_time").Int()}
	if result.Progress < 0 || result.Progress > 100 {
		return model.ImagineTask{}, fmt.Errorf("invalid task progress")
	}
	switch status {
	case "submitted", "queued", "pending":
		result.Status = "queued"
	case "processing", "in_progress":
		result.Status = "processing"
	case "completed", "succeeded", "success":
		result.Status = "completed"
		result.Progress = 100
	case "failed", "error", "cancelled", "canceled":
		result.Status = "failed"
	default:
		return model.ImagineTask{}, fmt.Errorf("unknown task status")
	}
	urls := []string{}
	seen := map[string]bool{}
	var addURL func(gjson.Result)
	addURL = func(v gjson.Result) {
		if v.IsArray() {
			for _, item := range v.Array() {
				addURL(item)
			}
			return
		}
		if v.IsObject() {
			addURL(v.Get("url"))
			addURL(v.Get("image_url"))
			if expires := v.Get("expires_at").Int(); expires > 0 && (result.ExpiresAt == 0 || expires < result.ExpiresAt) {
				result.ExpiresAt = expires
			}
			return
		}
		u := v.String()
		if IsValidMediaResultURL(u) && !seen[u] {
			seen[u] = true
			urls = append(urls, u)
		}
	}
	for _, path := range []string{"result.images", "result.image_urls", "image_urls", "result.url", "result.urls"} {
		addURL(root.Get(path))
	}
	for _, path := range []string{"grid_image_url", "result.grid_image_url"} {
		if u := root.Get(path).String(); IsValidMediaResultURL(u) {
			result.GridURL = u
		}
	}
	if expires := root.Get("expires_at").Int(); expires > 0 {
		result.ExpiresAt = expires
	}
	if result.Status == "completed" && len(urls) == 0 && result.GridURL == "" {
		return model.ImagineTask{}, fmt.Errorf("completed task has no images")
	}
	if result.Status == "completed" && cacheImages {
		for i, u := range urls {
			urls[i] = CacheImageLocally(u)
		}
		if result.GridURL != "" {
			result.GridURL = CacheImageLocally(result.GridURL)
		}
	}
	encoded, err := common.Marshal(urls)
	if err != nil {
		return model.ImagineTask{}, err
	}
	result.Images = string(encoded)
	result.ErrorCode = root.Get("error.code").String()
	result.ErrorMessage = root.Get("error.message").String()
	result.ErrorType = root.Get("error.type").String()
	result.ErrorParam = root.Get("error.param").String()
	return result, nil
}

func QueryImagineTask(ctx context.Context, task *model.ImagineTask) error {
	if task.Status == "completed" || task.Status == "failed" {
		return nil
	}
	if task.CallbackPayload != "" {
		return ProcessImagineResult(task, []byte(task.CallbackPayload))
	}
	batch, err := model.GetImagineBatch(task.BatchID)
	if err != nil {
		return err
	}
	channel, err := model.GetChannelById(batch.ChannelID, true)
	if err != nil {
		return err
	}
	key, _, apiErr := channel.GetNextEnabledKey()
	if apiErr != nil {
		return fmt.Errorf("task channel unavailable")
	}
	endpoint := strings.TrimRight(channel.GetBaseURL(), "/") + "/v1/tasks/" + url.PathEscape(task.UpstreamID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("task query returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	return ProcessImagineResult(task, body)
}

// Persist a bounded callback before acknowledging it; no image download or billing here.
func SaveImagineCallback(token string, body []byte) (int, error) {
	if len(token) != 64 || len(body) > 4<<20 || !gjson.ValidBytes(body) {
		return http.StatusBadRequest, fmt.Errorf("invalid callback")
	}
	var batch model.ImagineBatch
	if err := model.DB.Where("callback_token = ?", token).First(&batch).Error; err != nil {
		return http.StatusForbidden, fmt.Errorf("invalid callback")
	}
	root := gjson.ParseBytes(body)
	if root.Get("data").IsObject() {
		root = root.Get("data")
	}
	id := root.Get("id").String()
	status := root.Get("status").String()
	if id == "" || (status != "completed" && status != "failed") {
		return http.StatusBadRequest, fmt.Errorf("invalid callback result")
	}
	var task model.ImagineTask
	if err := model.DB.Where("batch_id = ? AND provider = ? AND upstream_id = ?", batch.ID, "apimart", id).First(&task).Error; err != nil {
		if batch.Status == "submitting" || batch.Status == "submission_unknown" {
			return http.StatusServiceUnavailable, fmt.Errorf("task submission pending")
		}
		return http.StatusNotFound, fmt.Errorf("task not found")
	}
	if task.Status == "completed" || task.Status == "failed" {
		return http.StatusOK, nil
	}
	if _, err := parseImagineResult(&task, body, false); err != nil {
		return http.StatusBadRequest, err
	}
	err := model.DB.Model(&model.ImagineTask{}).Where("id = ? AND status NOT IN ? AND (callback_payload = ? OR callback_payload IS NULL)", task.ID, []string{"completed", "failed"}, "").
		Updates(map[string]any{"callback_payload": root.Raw, "callback_received_at": time.Now().Unix(), "next_poll_at": time.Now().Unix()}).Error
	if err != nil {
		return http.StatusServiceUnavailable, err
	}
	return http.StatusOK, nil
}

func StartImagineTaskWorker() {
	if !common.IsMasterNode {
		return
	}
	go func() {
		for {
			RunImagineTaskWorkerOnce()
			time.Sleep(15 * time.Second)
		}
	}()
}

func RunImagineTaskWorkerOnce() {
	var batches []model.ImagineBatch
	if model.DB.Where("status IN ? AND created_at < ?", []string{"submitting", "submission_unknown"}, time.Now().Unix()-180).Limit(40).Find(&batches).Error == nil {
		for _, batch := range batches {
			var ids []string
			for _, item := range gjson.Get(batch.SubmissionResponse, "data").Array() {
				if id := item.Get("task_id").String(); id != "" {
					ids = append(ids, id)
				}
			}
			if len(ids) > 0 {
				_ = model.AttachImagineTasks(batch.ID, ids)
			} else {
				_ = model.DB.Model(&model.ImagineBatch{}).Where("id = ? AND status = ?", batch.ID, "submitting").Update("status", "submission_unknown").Error
			}
		}
	}
	var tasks []model.ImagineTask
	now := time.Now().Unix()
	if err := model.DB.Where("status IN ? AND next_poll_at <= ?", []string{"queued", "processing"}, now).Order("next_poll_at").Limit(40).Find(&tasks).Error; err != nil {
		logger.LogWarn(context.Background(), "imagine worker database unavailable")
		return
	}
	for _, task := range tasks {
		// A database lease also bounds active user queries and recovers after a worker crash.
		claim := model.DB.Model(&model.ImagineTask{}).Where("id = ? AND next_poll_at <= ?", task.ID, now).Update("next_poll_at", now+180)
		if claim.Error != nil || claim.RowsAffected == 0 {
			continue
		}
		if err := QueryImagineTask(context.Background(), &task); err != nil {
			logger.LogWarn(context.Background(), fmt.Sprintf("imagine task pending task_id=%s query_error=%v", task.ID, err))
			_ = model.DB.Model(&model.ImagineTask{}).Where("id = ?", task.ID).Update("next_poll_at", time.Now().Unix()+300).Error
		}
	}
	if err := model.DeliverImagineBillingEvents(); err != nil {
		logger.LogWarn(context.Background(), "imagine billing journal delivery failed: "+err.Error())
	}
}
