package apimartomni

import (
	"bytes"
	"context"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const Model = "gemini-omni-1.1-flash"
const Ext = "gemini-omni-1.1-flash-ext"
const Platform constant.TaskPlatform = "apimart-omni"

// Google's published 720p output conversion, excluding input/text tokens.
const BasePrice = 5792.0 * 17.5 / 1000000
const OfficialGoldBase = .088
const ExtGoldBase = .05 // 720p, six seconds ($0.30/task).
type TaskAdaptor struct {
	taskcommon.BaseBilling
	baseURL, apiKey string
}

func IsModel(name string) bool { return name == Model }
func IsChannel(base string) bool {
	u, e := url.Parse(base)
	return e == nil && (strings.EqualFold(u.Hostname(), "api.apimart.ai") || strings.EqualFold(u.Hostname(), "api.apib.ai"))
}
func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.baseURL, a.apiKey = strings.TrimRight(info.ChannelBaseUrl, "/"), info.ApiKey
}

func goldPrice(res string, seconds int, ext, video bool) float64 {
	if !ext {
		return map[string]float64{"360p": .0296, "720p": .088, "1080p": .132, "4k": .264}[res] * float64(seconds)
	}
	if video {
		return map[string]float64{"360p": .04, "720p": .08, "1080p": .08, "4k": .24}[res] * float64(seconds)
	}
	base := map[string]float64{"360p": .15, "720p": .25, "1080p": .25, "4k": .75}[res]
	step := .05
	if res == "360p" {
		step = .025
	}
	return base + float64(seconds-4)/2*step
}
func stringURLs(fields map[string]any, key string, limit int) ([]string, error) {
	value, exists := fields[key]
	if !exists {
		return nil, nil
	}
	raw, e := common.Marshal(value)
	if e != nil {
		return nil, e
	}
	var urls []string
	if common.Unmarshal(raw, &urls) != nil || len(urls) > limit {
		return nil, fmt.Errorf("%s supports at most %d public URLs", key, limit)
	}
	for _, s := range urls {
		u, e := url.Parse(s)
		if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, fmt.Errorf("%s requires HTTP(S) URLs", key)
		}
	}
	return urls, nil
}
func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	fail := func(e error) *dto.TaskError { return service.TaskErrorWrapperLocal(e, "invalid_request", 400) }
	capability := func(s string) *dto.TaskError {
		return service.TaskErrorWrapperLocal(fmt.Errorf("%s", s), "omni_route_unsupported", 503)
	}
	if !strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
		return fail(fmt.Errorf("Gemini Omni requires application/json"))
	}
	var f map[string]any
	if e := common.UnmarshalBodyReusable(c, &f); e != nil || f == nil {
		return fail(fmt.Errorf("invalid JSON object"))
	}
	name, _ := f["model"].(string)
	if !IsModel(name) {
		return fail(fmt.Errorf("unsupported model"))
	}
	info.OriginModelName, info.UpstreamModelName = name, name
	if e := helper.ModelMappedHelper(c, info, nil); e != nil {
		return fail(e)
	}
	ext := info.UpstreamModelName == Ext
	if !ext && info.UpstreamModelName != Model {
		return fail(fmt.Errorf("invalid Omni model mapping"))
	}
	allowed := map[string]bool{}
	for _, key := range strings.Fields("model prompt duration seconds resolution aspect_ratio ratio size image_urls first_frame_image last_frame_image image_with_roles video_urls metadata extend_from_task_id generation_type nsfw_check") {
		allowed[key] = true
	}
	for key := range f {
		if !allowed[key] {
			return fail(fmt.Errorf("unsupported field %s", key))
		}
	}
	resolution := "720p"
	if v, ok := f["resolution"]; ok {
		s, ok := v.(string)
		if !ok {
			return fail(fmt.Errorf("resolution must be string"))
		}
		resolution = strings.ToLower(s)
	}
	if resolution == "2160p" && !ext {
		resolution = "4k"
	}
	if resolution != "360p" && resolution != "720p" && resolution != "1080p" && resolution != "4k" {
		return fail(fmt.Errorf("resolution must be 360p, 720p, 1080p or 4k"))
	}
	aspect := "16:9"
	aspectSet := false
	for _, k := range []string{"aspect_ratio", "ratio", "size"} {
		if v, ok := f[k]; ok {
			s, ok := v.(string)
			if !ok || (s != "16:9" && s != "9:16") {
				return fail(fmt.Errorf("%s must be 16:9 or 9:16", k))
			}
			if aspectSet && aspect != s {
				return fail(fmt.Errorf("conflicting aspect ratios"))
			}
			aspect = s
			aspectSet = true
		}
	}
	images, e := stringURLs(f, "image_urls", 10)
	if e != nil {
		return fail(e)
	}
	videos, e := stringURLs(f, "video_urls", 1)
	if e != nil {
		return fail(e)
	}
	for _, k := range []string{"first_frame_image", "last_frame_image"} {
		if v, ok := f[k]; ok {
			if _, e := stringURLs(map[string]any{k: []any{v}}, k, 1); e != nil {
				return fail(e)
			}
		}
	}
	if _, last := f["last_frame_image"]; last && f["first_frame_image"] == nil {
		return fail(fmt.Errorf("last_frame_image requires first_frame_image"))
	}
	if f["image_with_roles"] != nil {
		if f["first_frame_image"] != nil || f["last_frame_image"] != nil {
			return fail(fmt.Errorf("use either image_with_roles or explicit frames"))
		}
		raw, _ := common.Marshal(f["image_with_roles"])
		var roles []struct {
			URL  string `json:"url"`
			Role string `json:"role"`
		}
		if common.Unmarshal(raw, &roles) != nil {
			return fail(fmt.Errorf("invalid image_with_roles"))
		}
		first, last := false, false
		for _, r := range roles {
			if _, e := stringURLs(map[string]any{"url": []string{r.URL}}, "url", 1); e != nil {
				return fail(e)
			}
			first = first || r.Role == "first_frame"
			last = last || r.Role == "last_frame"
		}
		if last && !first {
			return fail(fmt.Errorf("last frame requires first frame"))
		}
		if len(roles)+len(images) > 10 {
			return fail(fmt.Errorf("at most 10 images"))
		}
	}
	count := len(images)
	for _, k := range []string{"first_frame_image", "last_frame_image"} {
		if f[k] != nil {
			count++
		}
	}
	if count > 10 {
		return fail(fmt.Errorf("at most 10 images"))
	}
	if v, exists := f["prompt"]; exists {
		if _, ok := v.(string); !ok {
			return fail(fmt.Errorf("prompt must be a string"))
		}
	}
	prompt, _ := f["prompt"].(string)
	if strings.TrimSpace(prompt) == "" && count == 0 && f["image_with_roles"] == nil && len(videos) == 0 && f["extend_from_task_id"] == nil {
		return fail(fmt.Errorf("provide prompt or media"))
	}
	seconds := 10
	_, hasDuration := f["duration"]
	_, hasSeconds := f["seconds"]
	if hasDuration && hasSeconds && fmt.Sprint(f["duration"]) != fmt.Sprint(f["seconds"]) {
		return fail(fmt.Errorf("duration and seconds conflict"))
	}
	if !hasDuration && hasSeconds {
		f["duration"] = f["seconds"]
		hasDuration = true
	}
	if ext {
		if strings.TrimSpace(prompt) == "" {
			return fail(fmt.Errorf("prompt is required"))
		}
		if f["first_frame_image"] != nil || f["last_frame_image"] != nil || f["image_with_roles"] != nil || f["extend_from_task_id"] != nil || f["metadata"] != nil {
			return capability("this feature requires the official Gemini Omni route")
		}
		generation := "frame"
		if v, ok := f["generation_type"]; ok {
			generation, _ = v.(string)
		}
		if generation != "frame" && generation != "reference" {
			return fail(fmt.Errorf("generation_type must be frame or reference"))
		}
		if len(images) > 0 && !(len(images) == 1 || generation == "reference" && len(images) == 3) {
			return fail(fmt.Errorf("unsupported image count for generation_type"))
		}
		if v, ok := f["nsfw_check"]; ok {
			if _, ok := v.(bool); !ok {
				return fail(fmt.Errorf("nsfw_check must be boolean"))
			}
		}
		if len(videos) > 0 {
			if hasDuration {
				return fail(fmt.Errorf("duration and video_urls are mutually exclusive"))
			}
			n, e := probeSeconds(c.Request.Context(), videos[0])
			if e != nil || n <= 0 {
				return fail(fmt.Errorf("cannot measure reference video duration"))
			}
			seconds = n
			delete(f, "duration")
		} else {
			seconds = 6
			if hasDuration {
				n, e := strconv.Atoi(fmt.Sprint(f["duration"]))
				if e != nil {
					return fail(fmt.Errorf("duration must be an integer"))
				}
				seconds = n
			}
			if seconds != 4 && seconds != 6 && seconds != 8 && seconds != 10 {
				return fail(fmt.Errorf("duration must be 4, 6, 8 or 10"))
			}
			f["duration"] = seconds
		}
	} else {
		if hasDuration {
			return capability("fixed duration requires the Gemini Omni Ext route; official output length is automatic")
		}
		if f["generation_type"] != nil || f["nsfw_check"] != nil {
			return capability("generation_type and nsfw_check require the Ext route")
		}
		if md, ok := f["metadata"]; ok {
			m, ok := md.(map[string]any)
			if !ok {
				return fail(fmt.Errorf("metadata must be an object"))
			}
			for k, v := range m {
				if k != "task" {
					return fail(fmt.Errorf("unsupported metadata field"))
				}
				s, ok := v.(string)
				if !ok || !(s == "text_to_video" || s == "image_to_video" || s == "reference_to_video" || s == "edit" || s == "extend") {
					return fail(fmt.Errorf("invalid metadata.task"))
				}
			}
		}
		if value, ok := f["extend_from_task_id"]; ok {
			id, ok := value.(string)
			if !ok || id == "" || len(videos) > 0 {
				return fail(fmt.Errorf("extend_from_task_id requires a task ID and excludes video_urls"))
			}
			parent, found, e := model.GetByTaskId(info.UserId, id)
			if e != nil || !found || parent.Status != model.TaskStatusSuccess || parent.Platform != Platform || parent.Properties.UpstreamModelName != Model {
				return fail(fmt.Errorf("successful official Omni parent task required"))
			}
			if parent.ChannelId != info.ChannelId {
				return capability("continuation must use its original channel")
			}
			f["extend_from_task_id"] = parent.GetUpstreamTaskID()
		}
		if len(videos) > 0 {
			n, e := probeSeconds(c.Request.Context(), videos[0])
			if e != nil || n <= 0 || n > 10 {
				return fail(fmt.Errorf("reference video must be measurable and at most 10 seconds"))
			}
		}
	}
	f["model"], f["resolution"], f["aspect_ratio"] = info.UpstreamModelName, resolution, aspect
	delete(f, "seconds")
	delete(f, "ratio")
	delete(f, "size")
	c.Set("omni_request", f)
	c.Set("task_request", relaycommon.TaskSubmitReq{Model: Model, Prompt: prompt, Duration: seconds, Seconds: strconv.Itoa(seconds), Size: resolution, Images: images, Metadata: map[string]any{"resolution": resolution, "has_video": len(videos) > 0, "omni_ext": ext}})
	info.Action = constant.TaskActionTextGenerate
	if count > 0 {
		info.Action = constant.TaskActionGenerate
	}
	return nil
}
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	req, e := relaycommon.GetTaskRequest(c)
	if e != nil {
		return nil
	}
	res, _ := req.Metadata["resolution"].(string)
	ext, _ := req.Metadata["omni_ext"].(bool)
	video, _ := req.Metadata["has_video"].(bool)
	base := OfficialGoldBase
	if ext {
		base = ExtGoldBase
	}
	return map[string]float64{"seconds": float64(req.Duration), "size": goldPrice(res, req.Duration, ext, video) / float64(req.Duration) / base}
}
func (a *TaskAdaptor) AdjustBillingOnComplete(task *model.Task, result *relaycommon.TaskInfo) int {
	if task == nil || task.PrivateData.BillingContext == nil || result == nil || result.Status != model.TaskStatusSuccess {
		return 0
	}
	bc := task.PrivateData.BillingContext
	cost := gjson.GetBytes(task.Data, "data.cost").Float()
	if cost <= 0 {
		cost = gjson.GetBytes(task.Data, "cost").Float()
	}
	if cost <= 0 {
		cost = gjson.GetBytes(task.Data, "data.credits_cost").Float() / 10
	}
	base := OfficialGoldBase
	if task.Properties.UpstreamModelName == Ext {
		base = ExtGoldBase
	}
	// The frozen customer unit price already contains channel procurement and
	// retail coefficients. Terminal USD cost includes input/output tokens on the
	// official route and the exact duration/resolution tier on Ext.
	if cost > 0 && bc.ModelPrice > 0 {
		return int(math.Round(cost / base * bc.ModelPrice * bc.GroupRatio * common.QuotaPerUnit))
	}
	if task.Properties.UpstreamModelName == Ext {
		return task.Quota
	}
	// Never settle an automatic duration using the ten-second reservation.
	seconds := 0.
	for _, p := range []string{"data.output_duration", "data.duration", "data.result.duration"} {
		if n := gjson.GetBytes(task.Data, p).Float(); n > 0 {
			seconds = n
			break
		}
	}
	if seconds <= 0 && result.Url != "" {
		n, e := probeSeconds(context.Background(), result.Url)
		if e == nil {
			seconds = float64(n)
		}
	}
	if seconds > 0 && bc.OtherRatios["seconds"] > 0 {
		return int(math.Round(float64(task.Quota) * seconds / bc.OtherRatios["seconds"]))
	}
	return 0
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
	body, ok := c.Get("omni_request")
	if !ok {
		return nil, fmt.Errorf("Gemini Omni request was not validated")
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
		return "", nil, service.TaskErrorWrapper(fmt.Errorf("upstream rejected the Gemini Omni request before task creation"), "upstream_rejected", http.StatusBadRequest)
	}
	if err != nil || !gjson.ValidBytes(raw) || gjson.GetBytes(raw, "code").Int() != 200 || id == "" {
		return "", nil, service.TaskErrorWrapper(fmt.Errorf("invalid Gemini Omni submission response"), "invalid_response", http.StatusBadGateway)
	}
	c.JSON(http.StatusOK, gin.H{"id": info.PublicTaskID, "task_id": info.PublicTaskID, "object": "video", "model": info.OriginModelName, "status": "queued"})
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
		return nil, fmt.Errorf("invalid Gemini Omni task response")
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
			return nil, fmt.Errorf("completed Gemini Omni task has no video URL")
		}
	case "failed", "failure", "error", "canceled", "cancelled":
		result.Status = model.TaskStatusFailure
		result.Progress = "100%"
		result.Reason = data.Get("error.message").String()
		if result.Reason == "" {
			result.Reason = "Gemini Omni generation failed"
		}
	default:
		return nil, fmt.Errorf("unrecognized Gemini Omni task status")
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
func (a *TaskAdaptor) GetModelList() []string { return []string{Model} }
func (a *TaskAdaptor) GetChannelName() string { return "APIMart Gemini Omni 3.1" }

func probeSeconds(ctx context.Context, raw string) (int, error) {
	n, e := service.ProbeRemoteVideoDurationSecondsRound(ctx, raw)
	return n, e
}
