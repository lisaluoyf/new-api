package gmiwan

import (
	"bytes"
	"fmt"
	"io"
	"math"
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
	"github.com/QuantumNous/new-api/relay/channel/task/sora"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const queuePath = "/api/v1/ie/requestqueue/apikey/requests"

// GMI's Wan route uses the request queue API, while our public API remains OpenAI compatible.
// Embed only the existing frozen per-second billing contract; transport is provider specific.
type TaskAdaptor struct {
	sora.TaskAdaptor
	baseURL string
	apiKey  string
}

func IsModel(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), "wan3.0-video-prime")
}
func IsChannel(base string) bool {
	u, err := url.Parse(base)
	return err == nil && (u.Hostname() == "api.gmi-serving.com" || u.Hostname() == "console.gmicloud.ai")
}
func queueBase(base string) string {
	if IsChannel(base) {
		return "https://console.gmicloud.ai"
	}
	return strings.TrimRight(base, "/")
}
func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.TaskAdaptor.Init(info)
	a.baseURL = queueBase(info.ChannelBaseUrl)
	a.apiKey = info.ApiKey
}

// Normalize before reserving money. Explicit false and zero remain intact.
func normalize(fields map[string]any) (map[string]any, error) {
	allowed := map[string]bool{}
	for _, k := range strings.Fields("model prompt negative_prompt resolution duration seconds ratio aspect_ratio size prompt_extend watermark seed first_frame last_frame reference_image input_reference audio_url") {
		allowed[k] = true
	}
	for k := range fields {
		if !allowed[k] {
			return nil, fmt.Errorf("unsupported Wan request field %s", k)
		}
	}
	prompt, ok := fields["prompt"].(string)
	if !ok || strings.TrimSpace(prompt) == "" || len([]rune(prompt)) > 5000 {
		return nil, fmt.Errorf("prompt must contain 1 to 5000 characters")
	}
	out := map[string]any{"prompt": prompt}
	seconds := 5
	if raw, exists := fields["duration"]; exists {
		n, ok := raw.(float64)
		if !ok || math.Trunc(n) != n || n < 2 || n > 30 {
			return nil, fmt.Errorf("duration must be an integer from 2 to 30")
		}
		seconds = int(n)
	}
	if raw, exists := fields["seconds"]; exists {
		text, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("seconds must be a string")
		}
		n, err := strconv.Atoi(text)
		if err != nil || n < 2 || n > 30 {
			return nil, fmt.Errorf("seconds must be from 2 to 30")
		}
		if _, exists := fields["duration"]; exists && n != seconds {
			return nil, fmt.Errorf("duration and seconds must agree")
		}
		seconds = n
	}
	resolution := "720P"
	if raw, exists := fields["resolution"]; exists {
		text, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("resolution must be a string")
		}
		resolution = strings.ToUpper(strings.TrimSpace(text))
	}
	if resolution != "480P" && resolution != "720P" && resolution != "1080P" {
		return nil, fmt.Errorf("resolution must be 480P, 720P or 1080P")
	}
	ratio := "16:9"
	for _, key := range []string{"aspect_ratio", "ratio"} {
		if raw, exists := fields[key]; exists {
			text, ok := raw.(string)
			if !ok {
				return nil, fmt.Errorf("%s must be a string", key)
			}
			if key == "ratio" && fields["aspect_ratio"] != nil && ratio != text {
				return nil, fmt.Errorf("ratio and aspect_ratio must agree")
			}
			ratio = text
		}
	}
	if ratio != "adaptive" && ratio != "16:9" && ratio != "9:16" && ratio != "1:1" && ratio != "4:3" && ratio != "3:4" {
		return nil, fmt.Errorf("unsupported aspect ratio")
	}
	if raw, exists := fields["size"]; exists {
		text, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("size must be a string")
		}
		// Public OpenAI dimensions are aliases; the explicit resolution controls the provider tier.
		dims := strings.Split(text, "x")
		if len(dims) != 2 {
			return nil, fmt.Errorf("size must be WIDTHxHEIGHT")
		}
		w, e1 := strconv.Atoi(dims[0])
		h, e2 := strconv.Atoi(dims[1])
		if e1 != nil || e2 != nil || w <= 0 || h <= 0 {
			return nil, fmt.Errorf("invalid size")
		}
		short := min(w, h)
		expected, _ := strconv.Atoi(strings.TrimSuffix(resolution, "P"))
		if short != expected {
			return nil, fmt.Errorf("size conflicts with resolution")
		}
	}
	out["duration"] = seconds
	out["resolution"] = resolution
	out["ratio"] = ratio
	for _, key := range []string{"prompt_extend", "watermark"} {
		if raw, exists := fields[key]; exists {
			if _, ok := raw.(bool); !ok {
				return nil, fmt.Errorf("%s must be a boolean", key)
			}
			out[key] = raw
		}
	}
	if raw, exists := fields["seed"]; exists {
		n, ok := raw.(float64)
		if !ok || math.Trunc(n) != n || n < 0 || n > 2147483647 {
			return nil, fmt.Errorf("seed must be an integer from 0 to 2147483647")
		}
		out["seed"] = raw
	}
	if raw, exists := fields["negative_prompt"]; exists {
		text, ok := raw.(string)
		if !ok || len([]rune(text)) > 500 {
			return nil, fmt.Errorf("negative_prompt must be a string of at most 500 characters")
		}
		out["negative_prompt"] = text
	}
	for _, key := range []string{"first_frame", "last_frame", "audio_url", "input_reference"} {
		if raw, exists := fields[key]; exists {
			text, ok := raw.(string)
			if !ok || !absoluteMediaURL(text) {
				return nil, fmt.Errorf("%s must be an absolute HTTP media URL", key)
			}
			target := key
			if key == "input_reference" {
				target = "first_frame"
				if v, ok := fields[target]; ok && v != text {
					return nil, fmt.Errorf("input_reference and first_frame conflict")
				}
			}
			out[target] = text
		}
	}
	if raw, exists := fields["reference_image"]; exists {
		list := []any{raw}
		if items, ok := raw.([]any); ok {
			list = items
		}
		if len(list) < 1 || len(list) > 10 {
			return nil, fmt.Errorf("reference_image requires 1 to 10 URLs")
		}
		for _, v := range list {
			text, ok := v.(string)
			if !ok || !absoluteMediaURL(text) {
				return nil, fmt.Errorf("reference_image must contain HTTP media URLs")
			}
		}
		out["reference_image"] = raw
	}
	return out, nil
}
func absoluteMediaURL(s string) bool {
	u, e := url.Parse(s)
	return e == nil && u.Host != "" && u.User == nil && (u.Scheme == "https" || u.Scheme == "http")
}
func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	var fields map[string]any
	if err := common.UnmarshalBodyReusable(c, &fields); err != nil {
		return service.TaskErrorWrapperLocal(fmt.Errorf("invalid JSON request"), "invalid_request", 400)
	}
	payload, err := normalize(fields)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", 400)
	}
	normalized := map[string]any{"model": fields["model"], "prompt": payload["prompt"], "duration": payload["duration"], "seconds": strconv.Itoa(payload["duration"].(int)), "resolution": payload["resolution"]}
	raw, err := common.Marshal(normalized)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", 400)
	}
	var req relaycommon.TaskSubmitReq
	if err = common.Unmarshal(raw, &req); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", 400)
	}
	c.Set("task_request", req)
	c.Set("gmi_wan_payload", payload)
	info.Action = constant.TaskActionTextGenerate
	return nil
}
func (a *TaskAdaptor) BuildRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	return a.baseURL + queuePath, nil
}
func (a *TaskAdaptor) BuildRequestHeader(_ *gin.Context, r *http.Request, _ *relaycommon.RelayInfo) error {
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json")
	r.Header.Set("Authorization", "Bearer "+a.apiKey)
	return nil
}
func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	payload, ok := c.Get("gmi_wan_payload")
	if !ok {
		return nil, fmt.Errorf("Wan request not validated")
	}
	raw, err := common.Marshal(map[string]any{"model": info.UpstreamModelName, "payload": payload})
	return bytes.NewReader(raw), err
}
func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, body io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, body)
}
func (a *TaskAdaptor) DoResponse(c *gin.Context, r *http.Response, info *relaycommon.RelayInfo) (string, []byte, *dto.TaskError) {
	defer r.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(r.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 || !gjson.ValidBytes(raw) {
		return "", nil, service.TaskErrorWrapper(fmt.Errorf("invalid GMI queue response"), "invalid_response", 502)
	}
	id := gjson.GetBytes(raw, "request_id").String()
	if id == "" {
		return "", nil, service.TaskErrorWrapper(fmt.Errorf("GMI queue did not return a request ID"), "invalid_response", 502)
	}
	if strings.HasSuffix(c.Request.URL.Path, "/videos/generations") {
		c.JSON(200, gin.H{"code": 200, "data": []gin.H{{"status": "submitted", "task_id": info.PublicTaskID}}})
	} else {
		video := dto.NewOpenAIVideo()
		video.ID = info.PublicTaskID
		video.TaskID = info.PublicTaskID
		video.Model = info.OriginModelName
		video.CreatedAt = time.Now().Unix()
		c.JSON(200, video)
	}
	return id, raw, nil
}
func (a *TaskAdaptor) FetchTask(base, key string, body map[string]any, proxy string) (*http.Response, error) {
	id, ok := body["task_id"].(string)
	if !ok || id == "" {
		return nil, fmt.Errorf("invalid GMI task ID")
	}
	req, err := http.NewRequest(http.MethodGet, queueBase(base)+queuePath+"/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil || client == nil {
		return nil, fmt.Errorf("GMI query client unavailable")
	}
	local := *client
	local.Timeout = 30 * time.Second
	local.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := local.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GMI task query transport unavailable")
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	response.Body.Close()
	if readErr != nil || len(raw) > 4<<20 || response.StatusCode != 200 || !gjson.ValidBytes(raw) || gjson.GetBytes(raw, "request_id").String() != id {
		return nil, fmt.Errorf("invalid GMI task query response (HTTP %d)", response.StatusCode)
	}
	response.Body = io.NopCloser(bytes.NewReader(raw))
	return response, nil
}
func (a *TaskAdaptor) ParseTaskResult(raw []byte) (*relaycommon.TaskInfo, error) {
	if len(raw) > 4<<20 || !gjson.ValidBytes(raw) {
		return nil, fmt.Errorf("invalid GMI task response")
	}
	data := gjson.ParseBytes(raw)
	id := data.Get("request_id").String()
	if id == "" {
		return nil, fmt.Errorf("missing GMI request ID")
	}
	result := &relaycommon.TaskInfo{TaskID: id, Progress: "0%"}
	switch strings.ToLower(data.Get("status").String()) {
	case "queued", "dispatched", "pending":
		result.Status = model.TaskStatusQueued
	case "processing", "running":
		result.Status = model.TaskStatusInProgress
	case "success", "completed":
		result.Status = model.TaskStatusSuccess
		result.Progress = "100%"
		result.CompletedAt = data.Get("updated_at").Int()
		for _, path := range []string{"outcome.media_urls.0.url", "outcome.video_url"} {
			if u := data.Get(path).String(); absoluteMediaURL(u) {
				result.Url = u
				break
			}
		}
		if result.Url == "" {
			return nil, fmt.Errorf("completed GMI task has no video URL")
		}
	case "failed", "failure", "cancelled", "canceled":
		result.Status = model.TaskStatusFailure
		result.Progress = "100%"
		for _, path := range []string{"error.message", "outcome.error.message", "outcome.error", "error", "message"} {
			if value := data.Get(path); value.Type == gjson.String && value.String() != "" {
				result.Reason = value.String()
				break
			}
		}
		if result.Reason == "" {
			result.Reason = "GMI video generation failed"
		}
	default:
		return nil, fmt.Errorf("unrecognized GMI task status")
	}
	return result, nil
}
func (a *TaskAdaptor) ConvertToOpenAIVideo(task *model.Task) ([]byte, error) {
	video := dto.NewOpenAIVideo()
	video.ID = task.TaskID
	video.TaskID = task.TaskID
	video.Model = task.Properties.OriginModelName
	video.Status = task.Status.ToVideoStatus()
	video.CreatedAt = task.CreatedAt
	video.CompletedAt = task.FinishTime
	video.SetProgressStr(task.Progress)
	if task.Status == model.TaskStatusSuccess {
		video.SetMetadata("url", taskcommon.BuildProxyURL(task.TaskID))
	}
	if task.Status == model.TaskStatusFailure {
		video.Error = &dto.OpenAIVideoError{Code: "generation_failed", Message: service.PublicTaskFailure(task)}
	}
	return common.Marshal(video)
}
func (a *TaskAdaptor) GetModelList() []string { return []string{"wan3.0-video-prime"} }
func (a *TaskAdaptor) GetChannelName() string { return "GMI Wan" }
