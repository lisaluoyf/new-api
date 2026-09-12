package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

const imageReconcileExtraSec = 1200 // sync 超时后继续轮询最多 20 分钟

var imageReconcileClaim sync.Map // taskID -> struct{}，进程内防重复结算

// ScheduleImageTaskReconcile 在同步轮询超时后继续在后台等待上游终态：
// 成功则补记消费日志并结算预扣费；失败或彻底超时才退款并记错误日志。
func ScheduleImageTaskReconcile(c *gin.Context, relayInfo *relaycommon.RelayInfo, taskID string) {
	if relayInfo == nil || taskID == "" {
		return
	}
	if _, loaded := imageReconcileClaim.LoadOrStore(taskID, struct{}{}); loaded {
		return
	}

	holdImageBillingRefund(relayInfo)

	baseURL := strings.TrimRight(common.GetContextKeyString(c, constant.ContextKeyChannelBaseUrl), "/")
	apiKey := common.GetContextKeyString(c, constant.ContextKeyChannelKey)
	tokenName := c.GetString("token_name")
	logContent := imageLogContentFromRequest(relayInfo.Request)

	job := imageReconcileJob{
		relayInfo:   relayInfo,
		taskID:      taskID,
		baseURL:     baseURL,
		apiKey:      apiKey,
		tokenName:   tokenName,
		logExtra:    logContent,
		startedAt:   time.Now(),
		requestID:   c.GetString(common.RequestIdKey),
		requestData: ImageRequestDataFromContext(c),
	}

	gopool.Go(func() {
		runImageTaskReconcile(job)
	})
}

type imageReconcileJob struct {
	relayInfo   *relaycommon.RelayInfo
	taskID      string
	baseURL     string
	apiKey      string
	tokenName   string
	logExtra    []string
	startedAt   time.Time
	requestID   string
	requestData map[string]interface{}
}

func holdImageBillingRefund(relayInfo *relaycommon.RelayInfo) {
	if relayInfo == nil || relayInfo.Billing == nil {
		return
	}
	if session, ok := relayInfo.Billing.(*BillingSession); ok {
		session.HoldRefund()
	}
}

func releaseImageBillingRefund(relayInfo *relaycommon.RelayInfo) {
	if relayInfo == nil || relayInfo.Billing == nil {
		return
	}
	if session, ok := relayInfo.Billing.(*BillingSession); ok {
		session.ReleaseHoldRefund()
	}
}

func runImageTaskReconcile(job imageReconcileJob) {
	defer imageReconcileClaim.Delete(job.taskID)

	deadline := job.startedAt.Add(time.Duration(imageReconcileExtraSec) * time.Second)
	status, imageURL, failReason := pollUpstreamImageTaskStatus(job.baseURL, job.apiKey, job.taskID, deadline)

	switch status {
	case "succeeded", "success", "completed":
		finalizeImageReconcileSuccess(job, imageURL)
	case "failed", "error", "cancelled":
		if failReason == "" {
			failReason = fmt.Sprintf("upstream task %s failed", job.taskID)
		}
		finalizeImageReconcileFailure(job, failReason)
	default:
		finalizeImageReconcileFailure(job, fmt.Sprintf("image task %s reconcile timeout after sync poll", job.taskID))
	}
}

func pollUpstreamImageTaskStatus(baseURL, apiKey, taskID string, deadline time.Time) (status string, imageURL string, failReason string) {
	poll := pollUpstreamImageTaskResult(baseURL, apiKey, taskID, deadline)
	return poll.Status, poll.ImageURL, poll.DisplayFailReason()
}

func pollUpstreamImageTaskResult(baseURL, apiKey, taskID string, deadline time.Time) ImageTaskPollResult {
	for time.Now().Before(deadline) {
		poll, err := fetchImageTaskStatusOnce(baseURL, apiKey, taskID)
		if err != nil {
			logger.LogWarn(context.Background(), fmt.Sprintf("image reconcile poll error task=%s: %v", taskID, err))
			time.Sleep(4 * time.Second)
			continue
		}
		switch poll.Status {
		case "failed", "error", "cancelled", "succeeded", "success", "completed":
			return poll
		}
		time.Sleep(4 * time.Second)
	}
	return ImageTaskPollResult{Status: "timeout"}
}

// fetchImageTaskStatusOnce issues a single GET /v1/tasks/{taskID} against baseURL and
// parses the upstream status. An empty Status (not err) means the body didn't parse —
// callers should treat that as "still pending" and retry later.
func fetchImageTaskStatusOnce(baseURL, apiKey, taskID string) (ImageTaskPollResult, error) {
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	pollURL := fmt.Sprintf("%s/v1/tasks/%s", strings.TrimRight(baseURL, "/"), taskID)
	client := &http.Client{Timeout: 15 * time.Second}

	req, err := http.NewRequest(http.MethodGet, pollURL, nil)
	if err != nil {
		return ImageTaskPollResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return ImageTaskPollResult{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Data struct {
			Status string `json:"status"`
			Error  struct {
				Code    string `json:"code"`
				Message string `json:"message"`
				Type    string `json:"type"`
			} `json:"error"`
			Result struct {
				Images []imageTaskPollImage `json:"images"`
			} `json:"result"`
			URL         string  `json:"url"`
			B64         string  `json:"b64_json"`
			Cost        float64 `json:"cost"`
			CreditsCost float64 `json:"credits_cost"`
		} `json:"data"`
	}
	if common.Unmarshal(body, &result) != nil {
		return ImageTaskPollResult{}, nil
	}

	switch result.Data.Status {
	case "failed", "error", "cancelled":
		failCode, failReason := parseImageTaskUpstreamError(result.Data.Error.Code, result.Data.Error.Message)
		return ImageTaskPollResult{
			Status:       result.Data.Status,
			FailCode:     failCode,
			FailReason:   failReason,
			UpstreamCost: result.Data.Cost,
			CreditsCost:  result.Data.CreditsCost,
		}, nil
	case "succeeded", "success", "completed":
		if url := extractImageTaskURL(result.Data.URL, result.Data.Result.Images); url != "" {
			return ImageTaskPollResult{
				Status:       result.Data.Status,
				ImageURL:     url,
				ImageURLs:    extractImageTaskURLs(result.Data.URL, result.Data.Result.Images),
				UpstreamCost: result.Data.Cost,
				CreditsCost:  result.Data.CreditsCost,
			}, nil
		}
		if result.Data.B64 != "" {
			// Normalize to a data URI so callers can treat it exactly like a URL —
			// CacheImageLocally/RewriteImageResponseBody already no-op on "data:" prefixes.
			return ImageTaskPollResult{
				Status:    result.Data.Status,
				ImageURL:  "data:image/png;base64," + result.Data.B64,
				ImageURLs: []string{"data:image/png;base64," + result.Data.B64},
			}, nil
		}
		return ImageTaskPollResult{Status: result.Data.Status}, nil
	default:
		return ImageTaskPollResult{Status: result.Data.Status}, nil
	}
}

type imageTaskPollImage struct {
	URL any `json:"url"`
}

func extractImageTaskURL(flatURL string, images []imageTaskPollImage) string {
	urls := extractImageTaskURLs(flatURL, images)
	if len(urls) > 0 {
		return urls[0]
	}
	return ""
}

func extractImageTaskURLs(flatURL string, images []imageTaskPollImage) []string {
	if flatURL != "" {
		return []string{flatURL}
	}
	var urls []string
	for _, image := range images {
		switch v := image.URL.(type) {
		case string:
			if v != "" {
				urls = append(urls, v)
			}
		case []any:
			for _, value := range v {
				if s, ok := value.(string); ok && s != "" {
					urls = append(urls, s)
				}
			}
		}
	}
	return urls
}

func finalizeImageReconcileSuccess(job imageReconcileJob, imageURL string) {
	releaseImageBillingRefund(job.relayInfo)

	bg := reconcileBackgroundContext(job.relayInfo.UserId, job.tokenName)
	bg.Set(common.RequestIdKey, job.requestID)
	bg.Set("image_poll_task_id", job.taskID)
	if len(job.requestData) > 0 {
		bg.Set(imageRequestDataContextKey, job.requestData)
	} else if req, ok := job.relayInfo.Request.(*dto.ImageRequest); ok {
		SetImageRequestDataOnContext(bg, req)
	}
	// The request context is gone after a synchronous timeout. Preserve the exact
	// task's preview here so logs never have to guess from nearby cached files.
	if strings.HasPrefix(imageURL, "data:image/") {
		imageURL = CacheImageBase64Locally(imageURL)
	} else if imageURL != "" {
		imageURL = CacheImageLocallyWithHeaders(imageURL, map[string]string{"Authorization": "Bearer " + job.apiKey})
	}
	if imageURL != "" {
		bg.Set("image_result_url", imageURL)
	}
	usage := &dto.Usage{TotalTokens: 1, PromptTokens: 1}

	logExtra := append([]string(nil), job.logExtra...)
	logExtra = append(logExtra, fmt.Sprintf("同步轮询超时后补结算, task_id %s", job.taskID))

	PostTextConsumeQuota(bg, job.relayInfo, usage, logExtra)

	logger.LogInfo(context.Background(), fmt.Sprintf("image reconcile success task=%s user=%d", job.taskID, job.relayInfo.UserId))
}

func finalizeImageReconcileFailure(job imageReconcileJob, reason string) {
	releaseImageBillingRefund(job.relayInfo)

	bg := reconcileBackgroundContext(job.relayInfo.UserId, job.tokenName)
	if job.relayInfo.Billing != nil && job.relayInfo.Billing.NeedsRefund() {
		if err := job.relayInfo.Billing.RefundSync(bg); err != nil {
			common.SysLog(fmt.Sprintf("image reconcile refund failed task=%s user=%d: %s",
				job.taskID, job.relayInfo.UserId, err.Error()))
		}
	}

	channelId := 0
	channelType := 0
	if job.relayInfo.ChannelMeta != nil {
		channelId = job.relayInfo.ChannelMeta.ChannelId
		channelType = job.relayInfo.ChannelMeta.ChannelType
	}
	other := map[string]interface{}{
		"task_id":              job.taskID,
		"error_code":           "image_generation_timeout",
		"error_type":           "openai_error",
		"status_code":          408,
		"image_reconcile":      true,
		"image_reconcile_fail": reason,
		"channel_id":           channelId,
		"channel_type":         channelType,
	}

	useTime := int(time.Since(job.relayInfo.StartTime).Seconds())
	model.RecordErrorLog(bg, job.relayInfo.UserId, channelId,
		job.relayInfo.OriginModelName, job.tokenName,
		fmt.Sprintf("status_code=408, %s", reason),
		job.relayInfo.TokenId, useTime, job.relayInfo.IsStream, job.relayInfo.UsingGroup, other)

	logger.LogWarn(context.Background(), fmt.Sprintf("image reconcile failed task=%s: %s", job.taskID, reason))
}

func reconcileBackgroundContext(userId int, tokenName string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if tokenName != "" {
		c.Set("token_name", tokenName)
	}
	if userId > 0 {
		if username, err := model.GetUsernameById(userId, false); err == nil && username != "" {
			c.Set("username", username)
		}
	}
	return c
}

func imageLogContentFromRequest(req dto.Request) []string {
	imageReq, ok := req.(*dto.ImageRequest)
	if !ok || imageReq == nil {
		return nil
	}
	var parts []string
	if imageReq.Size != "" {
		parts = append(parts, fmt.Sprintf("大小 %s", imageReq.Size))
	}
	quality := imageReq.Quality
	if quality == "" {
		quality = "standard"
	}
	parts = append(parts, fmt.Sprintf("品质 %s", quality))
	imageN := uint(1)
	if imageReq.N != nil {
		imageN = *imageReq.N
	}
	if imageN > 0 {
		parts = append(parts, fmt.Sprintf("生成数量 %d", imageN))
	}
	return parts
}
