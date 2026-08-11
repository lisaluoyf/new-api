package hailuo

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

const (
	h3Model             = "MiniMax-H3"
	h3CreatePath        = "/v2/video_generation"
	h3QueryPath         = "/v2/query/video_generation/"
	h3DefaultResolution = "768P"
	h3DefaultRatio      = "16:9"
	h3DefaultDuration   = 4
)

// H3TaskAdaptor implements MiniMax-H3's v2 video API. It is deliberately
// separate from the legacy Hailuo adaptor: H3 uses a different request body,
// endpoint, status schema, and returns a CDN URL directly.
type H3TaskAdaptor struct {
	taskcommon.BaseBilling
	apiKey  string
	baseURL string
}

type h3Content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type h3CreateRequest struct {
	Model         string      `json:"model"`
	Content       []h3Content `json:"content"`
	Resolution    string      `json:"resolution"`
	Duration      int         `json:"duration"`
	Ratio         string      `json:"ratio"`
	CallbackURL   string      `json:"callback_url,omitempty"`
	AigcWatermark *bool       `json:"aigc_watermark,omitempty"`
}

type h3CreateResponse struct {
	TaskID string `json:"task_id"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type h3TaskResponse struct {
	Task struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Status  string `json:"status"`
		Content struct {
			URL string `json:"url"`
		} `json:"content"`
		Usage struct {
			OutputSeconds int `json:"output_seconds"`
		} `json:"usage"`
	} `json:"task"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (a *H3TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.baseURL = strings.TrimRight(info.ChannelBaseUrl, "/")
	a.apiKey = info.ApiKey
}

func (a *H3TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	return relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate)
}

func (a *H3TaskAdaptor) BuildRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	return a.baseURL + h3CreatePath, nil
}

func (a *H3TaskAdaptor) BuildRequestHeader(_ *gin.Context, req *http.Request, _ *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	return nil
}

func (a *H3TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	v, exists := c.Get("task_request")
	if !exists {
		return nil, fmt.Errorf("request not found in context")
	}
	req, ok := v.(relaycommon.TaskSubmitReq)
	if !ok {
		return nil, fmt.Errorf("invalid request type in context")
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("prompt is required for MiniMax-H3")
	}

	payload := h3CreateRequest{
		Model:      h3Model,
		Content:    []h3Content{{Type: "text", Text: req.Prompt}},
		Resolution: h3DefaultResolution,
		Duration:   h3DefaultDuration,
		Ratio:      h3DefaultRatio,
	}
	if req.Duration >= 4 && req.Duration <= 15 {
		payload.Duration = req.Duration
	}
	if strings.Contains(strings.ToUpper(req.Size), "2K") {
		payload.Resolution = "2K"
	}
	if req.Metadata != nil {
		if err := req.UnmarshalMetadata(&payload); err != nil {
			return nil, errors.Wrap(err, "unmarshal H3 metadata")
		}
	}
	// Metadata must not be able to change the routed model or omit the prompt.
	payload.Model = h3Model
	payload.Content = []h3Content{{Type: "text", Text: req.Prompt}}
	if payload.Duration < 4 || payload.Duration > 15 {
		return nil, fmt.Errorf("MiniMax-H3 duration must be between 4 and 15 seconds")
	}
	if payload.Ratio == "" {
		payload.Ratio = h3DefaultRatio
	}

	data, err := common.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}

func (a *H3TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

func (a *H3TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (string, []byte, *dto.TaskError) {
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return "", nil, service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
	}
	var parsed h3CreateResponse
	if err := common.Unmarshal(body, &parsed); err != nil {
		return "", body, service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", body), "unmarshal_response_body_failed", http.StatusInternalServerError)
	}
	if parsed.TaskID == "" {
		message := "MiniMax-H3 did not return task_id"
		if parsed.Error != nil && parsed.Error.Message != "" {
			message = parsed.Error.Message
		}
		return "", body, service.TaskErrorWrapper(errors.New(message), "h3_task_id_missing", http.StatusBadRequest)
	}

	video := dto.NewOpenAIVideo()
	video.ID = info.PublicTaskID
	video.TaskID = info.PublicTaskID
	video.CreatedAt = time.Now().Unix()
	video.Model = h3Model
	c.JSON(http.StatusOK, video)
	return parsed.TaskID, body, nil
}

func (a *H3TaskAdaptor) FetchTask(baseURL, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok || strings.TrimSpace(taskID) == "" {
		return nil, fmt.Errorf("invalid task_id")
	}
	uri := strings.TrimRight(baseURL, "/") + h3QueryPath + url.PathEscape(taskID)
	req, err := http.NewRequest(http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	return client.Do(req)
}

func (a *H3TaskAdaptor) GetModelList() []string { return []string{h3Model} }
func (a *H3TaskAdaptor) GetChannelName() string { return "minimax-h3" }

func (a *H3TaskAdaptor) ParseTaskResult(body []byte) (*relaycommon.TaskInfo, error) {
	var parsed h3TaskResponse
	if err := common.Unmarshal(body, &parsed); err != nil {
		return nil, errors.Wrap(err, "unmarshal H3 task result")
	}
	if parsed.Task.ID == "" {
		if parsed.Error != nil && parsed.Error.Message != "" {
			return relaycommon.FailTaskInfo(parsed.Error.Message), nil
		}
		return nil, fmt.Errorf("MiniMax-H3 task response missing task.id")
	}

	result := &relaycommon.TaskInfo{TaskID: parsed.Task.ID}
	if parsed.Task.Usage.OutputSeconds > 0 {
		result.BillableSeconds = parsed.Task.Usage.OutputSeconds
	}
	switch strings.ToLower(parsed.Task.Status) {
	case "queued":
		result.Status = model.TaskStatusQueued
		result.Progress = taskcommon.ProgressQueued
	case "running":
		result.Status = model.TaskStatusInProgress
		result.Progress = taskcommon.ProgressInProgress
	case "succeeded":
		result.Status = model.TaskStatusSuccess
		result.Progress = taskcommon.ProgressComplete
		result.Url = parsed.Task.Content.URL
	case "failed", "cancelled", "canceled":
		result.Status = model.TaskStatusFailure
		result.Progress = taskcommon.ProgressComplete
		result.Reason = "MiniMax-H3 task " + strings.ToLower(parsed.Task.Status)
	default:
		result.Status = model.TaskStatusInProgress
		result.Progress = taskcommon.ProgressInProgress
	}
	return result, nil
}

func (a *H3TaskAdaptor) ConvertToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	video := originTask.ToOpenAIVideo()
	if originTask.Status == model.TaskStatusFailure && video.Error == nil {
		video.Error = &dto.OpenAIVideoError{Message: originTask.FailReason, Code: strconv.Itoa(http.StatusBadRequest)}
	}
	return common.Marshal(video)
}
