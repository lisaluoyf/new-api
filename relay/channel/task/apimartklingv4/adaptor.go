package apimartklingv4

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
	"github.com/QuantumNous/new-api/pkg/klingv4"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const Platform constant.TaskPlatform = "apimart-kling-v4"

type TaskAdaptor struct {
	taskcommon.BaseBilling
	baseURL, apiKey string
}

func IsModel(name string) bool { return name == klingv4.Model }
func IsChannel(base string) bool {
	u, e := url.Parse(base)
	return e == nil && (strings.EqualFold(u.Hostname(), "api.apimart.ai") || strings.EqualFold(u.Hostname(), "api.apib.ai"))
}
func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.baseURL, a.apiKey = strings.TrimRight(info.ChannelBaseUrl, "/"), info.ApiKey
}

// Price uses the official Balance preview tariff unless an explicit local
// configuration overrides it. Malformed or incomplete overrides fail closed.
func Price(variant string) float64 {
	common.OptionMapRWMutex.RLock()
	raw := common.OptionMap["VideoModelPricing"]
	common.OptionMapRWMutex.RUnlock()
	var all map[string]struct {
		Prices map[string]float64 `json:"prices"`
	}
	if strings.TrimSpace(raw) != "" && common.UnmarshalJsonStr(raw, &all) != nil {
		return 0
	}
	prices := klingv4.OfficialPreviewPrices()
	if configured, ok := all[klingv4.Model]; ok {
		prices = configured.Prices
	}
	for k, p := range prices {
		if strings.EqualFold(k, variant) && p > 0 {
			return p
		}
	}
	return 0
}
func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	fail := func(e error) *dto.TaskError {
		return service.TaskErrorWrapperLocal(e, "invalid_request", http.StatusBadRequest)
	}
	if !strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
		return fail(fmt.Errorf("Kling V4 requires application/json"))
	}
	var fields map[string]any
	if e := common.UnmarshalBodyReusable(c, &fields); e != nil || fields == nil {
		return fail(fmt.Errorf("invalid Kling V4 JSON"))
	}
	if fields["model"] != klingv4.Model {
		return fail(fmt.Errorf("model must be kling-v4"))
	}
	spec, e := klingv4.Normalize(c.Request.Context(), fields, service.ProbeRemoteVideoDuration)
	if e != nil {
		return fail(e)
	}
	if Price(spec.Variant) <= 0 || Price("720p") <= 0 {
		return service.TaskErrorWrapperLocal(fmt.Errorf("Kling V4 pricing is not configured for this variant"), "model_price_error", http.StatusServiceUnavailable)
	}
	// Public callbacks are handled by our task lifecycle, never exposed upstream.
	delete(fields, "webhook")
	c.Set("kling_v4_request", fields)
	c.Set("kling_v4_spec", spec)
	prompt, _ := fields["prompt"].(string)
	aspect, _ := fields["aspect_ratio"].(string)
	c.Set("task_request", relaycommon.TaskSubmitReq{Model: klingv4.Model, Prompt: prompt, Duration: spec.Duration, Seconds: strconv.Itoa(spec.Duration), Size: spec.Resolution, Images: spec.Images, Metadata: map[string]any{"resolution": spec.Resolution, "aspect_ratio": aspect, "billing_variant": spec.Variant, "input_video_seconds": spec.InputSeconds, "billable_seconds": spec.BillableSeconds, "omni_reference_task_type": spec.Mode, "media_type": "video", "public_request": fields}})
	info.Action = constant.TaskActionTextGenerate
	if spec.Mode != "text" {
		info.Action = constant.TaskActionGenerate
	}
	return nil
}
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	v, ok := c.Get("kling_v4_spec")
	if !ok {
		return nil
	}
	spec := v.(klingv4.Spec)
	base := Price("720p")
	if base <= 0 {
		return nil
	}
	return map[string]float64{"seconds": float64(spec.BillableSeconds), "size": Price(spec.Variant) / base}
}
func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	return a.baseURL + "/v1/videos/generations", nil
}
func (a *TaskAdaptor) BuildRequestHeader(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	req.Header.Set("Content-Type", "application/json")
	return nil
}
func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	body, ok := c.Get("kling_v4_request")
	if !ok {
		return nil, fmt.Errorf("Kling V4 request was not validated")
	}
	out, err := common.Marshal(body)
	return bytes.NewReader(out), err
}
func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, body io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, body)
}
func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (string, []byte, *dto.TaskError) {
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	id := gjson.GetBytes(raw, "data.0.task_id").String()
	// APIMart can return HTTP 200 with success:false before creating a task.
	// Keep explicit rejection distinct from a malformed/ambiguous acceptance,
	// so the billing session refunds rather than holding the reservation.
	if err == nil && gjson.ValidBytes(raw) && id == "" && gjson.GetBytes(raw, "success").Type == gjson.False {
		return "", nil, service.TaskErrorWrapper(fmt.Errorf("upstream rejected the Kling V4 request before task creation"), "upstream_rejected", http.StatusBadRequest)
	}
	if err != nil || !gjson.ValidBytes(raw) || gjson.GetBytes(raw, "code").Int() != 200 || id == "" {
		return "", nil, service.TaskErrorWrapper(fmt.Errorf("invalid Kling V4 submission response"), "invalid_response", http.StatusBadGateway)
	}
	c.JSON(http.StatusOK, gin.H{"id": info.PublicTaskID, "task_id": info.PublicTaskID, "object": "video", "model": info.OriginModelName, "status": "queued", "created_at": time.Now().Unix()})
	return id, raw, nil
}
func (a *TaskAdaptor) FetchTask(base, key string, body map[string]any, proxy string) (*http.Response, error) {
	id, ok := body["task_id"].(string)
	if !ok || id == "" {
		return nil, fmt.Errorf("task_id required")
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(base, "/")+"/v1/tasks/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, err
	}
	return client.Do(req)
}
func (a *TaskAdaptor) ParseTaskResult(raw []byte) (*relaycommon.TaskInfo, error) {
	if !gjson.ValidBytes(raw) {
		return nil, fmt.Errorf("invalid Kling V4 task response")
	}
	data := gjson.GetBytes(raw, "data")
	result := &relaycommon.TaskInfo{Progress: "0%"}
	switch strings.ToLower(data.Get("status").String()) {
	case "submitted", "queued", "pending":
		result.Status = model.TaskStatusQueued
	case "processing", "running", "in_progress":
		result.Status = model.TaskStatusInProgress
	case "completed", "success", "succeeded":
		result.Status = model.TaskStatusSuccess
		result.Progress = "100%"
		for _, path := range []string{"result.videos.0.url.0", "result.videos.0.url", "result.video_url", "video_url"} {
			u := data.Get(path).String()
			parsed, err := url.Parse(u)
			if err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https") {
				result.Url = u
				break
			}
		}
		if result.Url == "" {
			return nil, fmt.Errorf("completed Kling V4 task has no video URL")
		}
	case "failed", "failure", "error", "canceled", "cancelled":
		result.Status = model.TaskStatusFailure
		result.Progress = "100%"
		result.Reason = data.Get("error.message").String()
		if result.Reason == "" {
			result.Reason = "Kling V4 generation failed"
		}
	default:
		return nil, fmt.Errorf("unrecognized Kling V4 task status")
	}
	return result, nil
}
func (a *TaskAdaptor) ConvertToOpenAIVideo(task *model.Task) ([]byte, error) {
	progress := dto.NewOpenAIVideo()
	progress.SetProgressStr(task.Progress)
	out := map[string]any{"id": task.TaskID, "task_id": task.TaskID, "object": "video", "model": task.Properties.OriginModelName, "status": task.Status.ToVideoStatus(), "progress": progress.Progress, "created_at": task.CreatedAt}
	if task.Status == model.TaskStatusSuccess {
		out["url"] = taskcommon.BuildProxyURL(task.TaskID)
		out["metadata"] = map[string]any{"url": taskcommon.BuildProxyURL(task.TaskID)}
	}
	if task.Status == model.TaskStatusFailure {
		out["error"] = map[string]string{"code": "generation_failed", "message": service.PublicTaskFailure(task)}
	}
	return common.Marshal(out)
}
func (a *TaskAdaptor) GetModelList() []string { return []string{klingv4.Model} }
func (a *TaskAdaptor) GetChannelName() string { return "Kling V4" }
