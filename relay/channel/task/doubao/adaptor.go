package doubao

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"github.com/samber/lo"
)

// ============================
// Request / Response structures
// ============================

type ContentItem struct {
	Type      string     `json:"type,omitempty"`
	Text      string     `json:"text,omitempty"`
	ImageURL  *MediaURL  `json:"image_url,omitempty"`
	VideoURL  *MediaURL  `json:"video_url,omitempty"`
	AudioURL  *MediaURL  `json:"audio_url,omitempty"`
	DraftTask *DraftTask `json:"draft_task,omitempty"`
	Role      string     `json:"role,omitempty"`
}

type DraftTask struct {
	ID string `json:"id"`
}

type MediaURL struct {
	URL string `json:"url,omitempty"`
}

type requestPayload struct {
	Moderation            *service.SeedanceModeration `json:"Moderation,omitempty"`
	Model                 string                      `json:"model"`
	Content               []ContentItem               `json:"content,omitempty"`
	CallbackURL           string                      `json:"callback_url,omitempty"`
	ReturnLastFrame       *dto.BoolValue              `json:"return_last_frame,omitempty"`
	ServiceTier           string                      `json:"service_tier,omitempty"`
	ExecutionExpiresAfter *dto.IntValue               `json:"execution_expires_after,omitempty"`
	GenerateAudio         *dto.BoolValue              `json:"generate_audio,omitempty"`
	Draft                 *dto.BoolValue              `json:"draft,omitempty"`
	Tools                 []struct {
		Type string `json:"type,omitempty"`
	} `json:"tools,omitempty"`
	Resolution            string         `json:"resolution,omitempty"`
	Ratio                 string         `json:"ratio,omitempty"`
	Duration              *dto.IntValue  `json:"duration,omitempty"`
	Frames                *dto.IntValue  `json:"frames,omitempty"`
	Seed                  *dto.IntValue  `json:"seed,omitempty"`
	CameraFixed           *dto.BoolValue `json:"camera_fixed,omitempty"`
	Watermark             *dto.BoolValue `json:"watermark,omitempty"`
	OutputFormat          *string        `json:"output_format,omitempty"`
	OmniReferenceTaskType *string        `json:"omni_reference_task_type,omitempty"`
}

type responsePayload struct {
	ID string `json:"id"` // task_id
}

type responseTask struct {
	ID       string `json:"id"`
	Model    string `json:"model"`
	Status   string `json:"status"`
	VideoURL string `json:"video_url"`
	Content  struct {
		VideoURL string `json:"video_url"`
	} `json:"content"`
	Seed            int             `json:"seed"`
	Resolution      string          `json:"resolution"`
	Duration        json.RawMessage `json:"duration"`
	Ratio           string          `json:"ratio"`
	FramesPerSecond int             `json:"framespersecond"`
	ServiceTier     string          `json:"service_tier"`
	Tools           []struct {
		Type string `json:"type"`
	} `json:"tools"`
	Usage struct {
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
		ToolUsage        struct {
			WebSearch int `json:"web_search"`
		} `json:"tool_usage"`
	} `json:"usage"`
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`
}

// ============================
// Adaptor implementation
// ============================

type TaskAdaptor struct {
	taskcommon.BaseBilling
	ChannelType int
	apiKey      string
	baseURL     string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.baseURL = info.ChannelBaseUrl
	a.apiKey = info.ApiKey
}

// ValidateRequestAndSetAction parses body, validates fields and sets default action.
func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) (taskErr *dto.TaskError) {
	// Middleware verifies draft ownership, age, model, channel and key before
	// setting this context. Upgrades reuse a draft and intentionally omit prompt.
	if value, exists := c.Get("seedance_draft_task"); exists {
		task, ok := value.(*model.Task)
		if !ok || task == nil || task.Properties.OriginModelName != "seedance-2.5" || task.PrivateData.UpstreamTaskID == "" {
			return service.TaskErrorWrapperLocal(fmt.Errorf("Draft is unavailable"), "invalid_request", http.StatusBadRequest)
		}
		info.Action = constant.TaskActionGenerate
		c.Set("task_request", relaycommon.TaskSubmitReq{Model: "seedance-2.5", Metadata: map[string]interface{}{}})
		return a.normalizeSeedanceRequest(c, info)
	}
	if taskErr = relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate); taskErr != nil {
		return taskErr
	}
	return a.normalizeSeedanceRequest(c, info)
}

// BuildRequestURL constructs the upstream URL.
func (a *TaskAdaptor) BuildRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	if service.IsCtaiSeedanceBaseURL(a.baseURL) {
		return "https://ai.ctaigw.cn/v1/contents/generations/tasks", nil
	}
	if service.IsTokenHubSeedanceBaseURL(a.baseURL) {
		return "https://ai-tokenhub.ai/api/v2/contents/generations/tasks", nil
	}
	return fmt.Sprintf("%s/api/v3/contents/generations/tasks", a.baseURL), nil
}

// BuildRequestHeader sets required headers.
func (a *TaskAdaptor) BuildRequestHeader(_ *gin.Context, req *http.Request, _ *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	return nil
}

// EstimateBilling 检测请求 metadata 中是否包含视频输入，返回视频折扣 OtherRatio。
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}
	if isSeedanceAlias(req.Model) {
		return seedanceBillingRatios(req)
	}
	if hasVideoInMetadata(req.Metadata) {
		if ratio, ok := GetVideoInputRatio(info.OriginModelName); ok {
			return map[string]float64{"video_input": ratio}
		}
	}
	return nil
}

// hasVideoInMetadata 直接检查 metadata 的 content 数组是否包含 video_url 条目，
// 避免构建完整的上游 requestPayload。
func hasVideoInMetadata(metadata map[string]interface{}) bool {
	if metadata == nil {
		return false
	}
	contentRaw, ok := metadata["content"]
	if !ok {
		return false
	}
	contentSlice, ok := contentRaw.([]interface{})
	if !ok {
		return false
	}
	for _, item := range contentSlice {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if itemMap["type"] == "video_url" {
			return true
		}
		if _, has := itemMap["video_url"]; has {
			return true
		}
	}
	return false
}

// BuildRequestBody converts request into Doubao specific format.
func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil, err
	}

	body, err := a.convertToRequestPayload(&req)
	if err != nil {
		return nil, errors.Wrap(err, "convert request payload failed")
	}
	if value, exists := c.Get("seedance_draft_task"); exists {
		task := value.(*model.Task)
		// Ark upgrades accept only a private draft reference plus final-output
		// controls. Re-sending inherited generation parameters is rejected.
		body.Content = []ContentItem{{Type: "draft_task", DraftTask: &DraftTask{ID: task.PrivateData.UpstreamTaskID}}}
		body.Duration, body.Frames, body.Seed = nil, nil, nil
		body.GenerateAudio, body.CameraFixed, body.Draft = nil, nil, nil
		body.Ratio, body.Tools, body.OmniReferenceTaskType = "", nil, nil
	} else {
		// This hint is only valid for multimodal reference generation. The
		// public normalizer's default "auto" must not reach text/frame tasks.
		references := false
		for _, item := range body.Content {
			if item.Type == "video_url" || item.Type == "audio_url" || (item.Type == "image_url" && item.Role == "reference_image") {
				references = true
			}
		}
		if !references {
			body.OmniReferenceTaskType = nil
		}
	}
	if info.IsModelMapped {
		body.Model = info.UpstreamModelName
	} else {
		info.UpstreamModelName = body.Model
	}
	data, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}
	// Resolve public APIMaster asset IDs only in the outbound body. The
	// normalized request and billing logs retain customer-visible identifiers.
	var fields map[string]any
	if err = common.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	fields, err = service.ResolveSeedanceAssetReferences(c, fields, info.ChannelId, a.apiKey)
	if err != nil {
		return nil, err
	}
	data, err = common.Marshal(fields)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}

// DoRequest delegates to common helper.
func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	if service.SupportsRetainedSeedanceAutomaticReview(info.ChannelId) {
		return service.DoXingchiAutomaticReview(c, info.ChannelId, info.OriginModelName, a.apiKey, info.PublicTaskID, requestBody, func(body io.Reader) (*http.Response, error) {
			resp, err := channel.DoTaskApiRequest(a, c, info, body)
			if err == nil && service.IsTokenHubSeedanceBaseURL(a.baseURL) {
				return service.NormalizeTokenHubVideoResponse(resp)
			}
			return resp, err
		})
	}
	if info.ChannelId != 272 || !service.IsSeedanceLibraryModel(info.OriginModelName) {
		return channel.DoTaskApiRequest(a, c, info, requestBody)
	}
	raw, err := io.ReadAll(requestBody)
	if err != nil {
		return nil, err
	}
	resp, err := channel.DoTaskApiRequest(a, c, info, bytes.NewReader(raw))
	if err != nil || resp == nil || resp.StatusCode != http.StatusBadRequest {
		return resp, err
	}
	rejected, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(rejected))
	if c.GetBool("seedance_automatic_review_attempted") || !service.ShouldAutomaticallyReviewSeedance(info.ChannelId, resp.StatusCode, info.OriginModelName, rejected) {
		return resp, nil
	}
	c.Set("seedance_automatic_review_attempted", true)
	// The first response explicitly rejected the video. Keep the same wallet
	// reservation and public task ID; never retry a transport-ambiguous POST.
	reviewed, reviewErr := service.PrepareAutomaticSeedanceReview(c, info.OriginModelName, a.apiKey, info.PublicTaskID, raw)
	if reviewErr != nil {
		code := "material_review_failed"
		if detail, ok := reviewErr.(*service.SeedanceAPIError); ok && detail.Code != "" {
			code = detail.Code
		}
		publicError := map[string]any{"code": code, "message": reviewErr.Error(), "type": "BadRequest"}
		if detail, ok := reviewErr.(*service.SeedanceAPIError); ok && detail.UpstreamError != nil {
			publicError["upstream_error"] = detail.UpstreamError
		}
		body, _ := common.Marshal(map[string]any{"error": publicError})
		resp.StatusCode = http.StatusBadRequest
		resp.Body = io.NopCloser(bytes.NewReader(body))
		resp.ContentLength = int64(len(body))
		return resp, nil
	}
	_ = resp.Body.Close()
	if err := service.MarkAutomaticSeedanceReviewSending(c.GetInt("id"), info.PublicTaskID, ""); err != nil {
		_ = service.QueueAutomaticSeedanceReviewCleanup(c.GetInt("id"), info.PublicTaskID)
		return nil, err
	}
	retry, err := channel.DoTaskApiRequest(a, c, info, bytes.NewReader(reviewed))
	if err != nil || retry == nil {
		return retry, err
	}
	if retry.StatusCode >= 400 && retry.StatusCode < 500 {
		_ = service.QueueAutomaticSeedanceReviewCleanup(c.GetInt("id"), info.PublicTaskID)
	}
	if retry.StatusCode == http.StatusOK {
		data, readErr := io.ReadAll(retry.Body)
		_ = retry.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		retry.Body = io.NopCloser(bytes.NewReader(data))
		var accepted struct {
			ID string `json:"id"`
		}
		if common.Unmarshal(data, &accepted) == nil && accepted.ID != "" {
			if persistErr := service.MarkAutomaticSeedanceReviewSending(c.GetInt("id"), info.PublicTaskID, accepted.ID); persistErr != nil {
				return nil, persistErr
			}
		}
	}
	return retry, nil
}

// DoResponse handles upstream response, returns taskID etc.
func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *dto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}
	_ = resp.Body.Close()

	// Parse Doubao response
	var dResp responsePayload
	if err := common.Unmarshal(responseBody, &dResp); err != nil {
		taskErr = service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", responseBody), "unmarshal_response_body_failed", http.StatusInternalServerError)
		return
	}

	if dResp.ID == "" {
		taskErr = service.TaskErrorWrapper(fmt.Errorf("task_id is empty"), "invalid_response", http.StatusInternalServerError)
		return
	}
	if isSeedanceAlias(info.OriginModelName) && strings.HasSuffix(c.Request.URL.Path, "/videos/generations") {
		c.JSON(http.StatusOK, gin.H{"code": 200, "data": []gin.H{{"status": "submitted", "task_id": info.PublicTaskID}}})
		return dResp.ID, responseBody, nil
	}

	ov := dto.NewOpenAIVideo()
	ov.ID = info.PublicTaskID
	ov.TaskID = info.PublicTaskID
	ov.CreatedAt = time.Now().Unix()
	ov.Model = info.OriginModelName

	c.JSON(http.StatusOK, ov)
	return dResp.ID, responseBody, nil
}

// FetchTask fetch task status
func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid task_id")
	}

	uri := fmt.Sprintf("%s/api/v3/contents/generations/tasks/%s", baseUrl, taskID)
	if service.IsCtaiSeedanceBaseURL(baseUrl) {
		uri = "https://ai.ctaigw.cn/v1/contents/generations/tasks/" + taskID
	}
	if service.IsTokenHubSeedanceBaseURL(baseUrl) {
		uri = "https://ai-tokenhub.ai/api/v2/contents/generations/tasks/" + taskID
	}

	req, err := http.NewRequest(http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	if !service.IsCtaiSeedanceBaseURL(baseUrl) {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+key)

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	resp, err := client.Do(req)
	if err == nil && service.IsTokenHubSeedanceBaseURL(baseUrl) {
		return service.NormalizeTokenHubVideoResponse(resp)
	}
	return resp, err
}

func (a *TaskAdaptor) GetModelList() []string {
	return ModelList
}

func (a *TaskAdaptor) GetChannelName() string {
	return ChannelName
}

func (a *TaskAdaptor) convertToRequestPayload(req *relaycommon.TaskSubmitReq) (*requestPayload, error) {
	r := requestPayload{
		Model:   req.Model,
		Content: []ContentItem{},
	}

	// Add images if present
	if req.HasImage() {
		for _, imgURL := range req.Images {
			r.Content = append(r.Content, ContentItem{
				Type: "image_url",
				ImageURL: &MediaURL{
					URL: imgURL,
				},
			})
		}
	}

	metadata := req.Metadata
	if err := taskcommon.UnmarshalMetadata(metadata, &r); err != nil {
		return nil, errors.Wrap(err, "unmarshal metadata failed")
	}

	if sec, _ := strconv.Atoi(req.Seconds); sec > 0 {
		r.Duration = lo.ToPtr(dto.IntValue(sec))
	}

	r.Content = lo.Reject(r.Content, func(c ContentItem, _ int) bool { return c.Type == "text" })
	r.Content = append(r.Content, ContentItem{
		Type: "text",
		Text: req.Prompt,
	})

	return &r, nil
}

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	resTask := responseTask{}
	if err := common.Unmarshal(respBody, &resTask); err != nil {
		return nil, errors.Wrap(err, "unmarshal task result failed")
	}
	var duration float64
	if len(resTask.Duration) > 0 && resTask.Status == "succeeded" {
		var err error
		duration, err = service.ParseSeedanceResponseDuration(resTask.Duration)
		if err != nil {
			return nil, err
		}
	}

	taskResult := relaycommon.TaskInfo{
		Code: 0,
	}

	// Map Doubao status to internal status
	switch resTask.Status {
	case "pending", "queued":
		taskResult.Status = model.TaskStatusQueued
		taskResult.Progress = "10%"
	case "processing", "running":
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "50%"
	case "succeeded":
		taskResult.Status = model.TaskStatusSuccess
		taskResult.Progress = "100%"
		taskResult.Url = resTask.Content.VideoURL
		if taskResult.Url == "" {
			taskResult.Url = resTask.VideoURL
		}
		if strings.TrimSpace(taskResult.Url) == "" {
			// Some compatible providers report success even when generation
			// produced no file. Fail and refund instead of saving a broken URL.
			taskResult.Status = model.TaskStatusFailure
			taskResult.Reason = "Video generation returned no video file"
			return &taskResult, nil
		}
		// 解析 usage 信息用于按倍率计费
		taskResult.CompletionTokens = resTask.Usage.CompletionTokens
		taskResult.TotalTokens = resTask.Usage.TotalTokens
		taskResult.BillableSeconds = int(math.Round(duration))
	case "failed":
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = "100%"
		taskResult.Reason = resTask.Error.Message
	default:
		// Unknown status, treat as processing
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "30%"
	}

	return &taskResult, nil
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	var dResp responseTask
	if err := common.Unmarshal(originTask.Data, &dResp); err != nil {
		return nil, errors.Wrap(err, "unmarshal doubao task data failed")
	}

	openAIVideo := dto.NewOpenAIVideo()
	openAIVideo.ID = originTask.TaskID
	openAIVideo.TaskID = originTask.TaskID
	openAIVideo.Status = originTask.Status.ToVideoStatus()
	openAIVideo.SetProgressStr(originTask.Progress)
	openAIVideo.SetMetadata("url", dResp.Content.VideoURL)
	openAIVideo.CreatedAt = originTask.CreatedAt
	openAIVideo.CompletedAt = originTask.UpdatedAt
	openAIVideo.Model = originTask.Properties.OriginModelName
	if isSeedanceAlias(openAIVideo.Model) {
		openAIVideo.SetMetadata("url", taskcommon.BuildProxyURL(originTask.TaskID))
	}

	if originTask.Status == model.TaskStatusFailure {
		openAIVideo.Error = &dto.OpenAIVideoError{
			Message: "Video generation failed",
			Code:    "generation_failed",
		}
	}

	return common.Marshal(openAIVideo)
}
