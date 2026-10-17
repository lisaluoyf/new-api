package apimartveo

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
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const Standard = "veo-3.1-generate-preview"
const Fast = "veo-3.1-fast-generate-preview"
const Platform constant.TaskPlatform = "apimart-veo"

type TaskAdaptor struct {
	taskcommon.BaseBilling
	baseURL, apiKey string
}

func IsModel(name string) bool { return name == Standard || name == Fast }
func IsChannel(base string) bool {
	u, err := url.Parse(base)
	return err == nil && (strings.EqualFold(u.Hostname(), "api.apimart.ai") || strings.EqualFold(u.Hostname(), "api.apib.ai"))
}
func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.baseURL, a.apiKey = strings.TrimRight(info.ChannelBaseUrl, "/"), info.ApiKey
}

// Google official USD/second tariff. Channel procurement coefficients and user
// discounts are applied by the existing price/billing pipeline, not here.
func OfficialPrice(name, resolution string, audio bool) float64 {
	prices := map[string]float64{"720p": .4, "1080p": .4, "4k": .6, "720p-silent": .2, "1080p-silent": .2, "4k-silent": .4}
	if name == Fast {
		prices = map[string]float64{"720p": .1, "1080p": .12, "4k": .3, "720p-silent": .08, "1080p-silent": .1, "4k-silent": .25}
	}
	key := strings.ToLower(resolution)
	if !audio {
		key += "-silent"
	}
	common.OptionMapRWMutex.RLock()
	raw := common.OptionMap["VideoModelPricing"]
	common.OptionMapRWMutex.RUnlock()
	var configured map[string]struct {
		Prices map[string]float64 `json:"prices"`
	}
	if common.UnmarshalJsonStr(raw, &configured) == nil {
		for variant, price := range configured[name].Prices {
			if strings.EqualFold(variant, key) && price > 0 {
				return price
			}
		}
	}
	return prices[key]
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	fail := func(err error) *dto.TaskError {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	// No upstream dispatch or charge has happened. A local 503 lets the task
	// router try the other provider variant without penalizing channel health.
	capability := func(message string) *dto.TaskError {
		return service.TaskErrorWrapperLocal(fmt.Errorf("%s", message), "veo_route_unsupported", http.StatusServiceUnavailable)
	}
	if !strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
		return fail(fmt.Errorf("Veo requires application/json"))
	}
	var fields map[string]any
	if err := common.UnmarshalBodyReusable(c, &fields); err != nil || fields == nil {
		return fail(fmt.Errorf("invalid Veo JSON request"))
	}
	name, _ := fields["model"].(string)
	if name == "" {
		name = info.OriginModelName
	}
	if !IsModel(name) || info.OriginModelName != "" && info.OriginModelName != name {
		return fail(fmt.Errorf("unsupported or conflicting Veo model"))
	}
	info.OriginModelName, info.UpstreamModelName = name, name
	if err := helper.ModelMappedHelper(c, info, nil); err != nil {
		return fail(err)
	}
	upstream := info.UpstreamModelName
	want := "veo3.1-quality"
	if name == Fast {
		want = "veo3.1-fast"
	}
	official := upstream == want+"-official"
	if !official && upstream != want && upstream != want+"-ext" {
		return fail(fmt.Errorf("Veo channel model mapping is not configured"))
	}
	allowed := map[string]bool{}
	for _, key := range strings.Fields("model prompt duration seconds resolution size aspect_ratio ratio image_urls input_reference first_frame_image last_frame_image generation_type generate_audio negative_prompt seed sample_count person_generation resize_mode enhance_prompt raw nsfw_check metadata") {
		allowed[key] = true
	}
	for key := range fields {
		if !allowed[key] {
			return fail(fmt.Errorf("unsupported Veo field %s", key))
		}
	}
	if metadata, ok := fields["metadata"].(map[string]any); ok {
		for key, value := range metadata {
			if !allowed[key] || key == "model" || key == "metadata" {
				return fail(fmt.Errorf("unsupported Veo metadata field %s", key))
			}
			if existing, exists := fields[key]; exists && fmt.Sprint(existing) != fmt.Sprint(value) {
				return fail(fmt.Errorf("conflicting Veo metadata %s", key))
			}
			fields[key] = value
		}
	} else if fields["metadata"] != nil {
		return fail(fmt.Errorf("metadata must be an object"))
	}
	prompt, ok := fields["prompt"].(string)
	if !ok || strings.TrimSpace(prompt) == "" {
		return fail(fmt.Errorf("prompt is required"))
	}
	seconds := 8
	if value, exists := fields["duration"]; exists {
		n, err := strconv.Atoi(fmt.Sprint(value))
		if err != nil {
			return fail(fmt.Errorf("duration must be an integer"))
		}
		seconds = n
	}
	if value, exists := fields["seconds"]; exists {
		n, err := strconv.Atoi(fmt.Sprint(value))
		if err != nil {
			return fail(fmt.Errorf("seconds must be an integer"))
		}
		if _, exists := fields["duration"]; exists && n != seconds {
			return fail(fmt.Errorf("duration and seconds conflict"))
		}
		seconds = n
	}
	if seconds != 4 && seconds != 6 && seconds != 8 {
		return fail(fmt.Errorf("Veo duration must be 4, 6 or 8 seconds"))
	}
	if !official && seconds != 8 {
		return capability("this duration requires the official Veo channel")
	}
	resolution := "720p"
	if value, exists := fields["resolution"]; exists {
		text, ok := value.(string)
		if !ok {
			return fail(fmt.Errorf("resolution must be a string"))
		}
		resolution = strings.ToLower(text)
	}
	aspect := "16:9"
	if ratio, exists := fields["ratio"]; exists {
		if aspect, exists := fields["aspect_ratio"]; exists && ratio != aspect {
			return fail(fmt.Errorf("ratio and aspect_ratio conflict"))
		}
	}
	for _, key := range []string{"ratio", "aspect_ratio"} {
		if value, exists := fields[key]; exists {
			text, ok := value.(string)
			if !ok || text != "16:9" && text != "9:16" {
				return fail(fmt.Errorf("aspect_ratio must be 16:9 or 9:16"))
			}
			if aspect != "16:9" && aspect != text {
				return fail(fmt.Errorf("aspect ratio conflict"))
			}
			aspect = text
		}
	}
	if size, exists := fields["size"]; exists {
		text, ok := size.(string)
		if !ok {
			return fail(fmt.Errorf("size must be a string"))
		}
		res, ar := "", ""
		switch strings.ToLower(text) {
		case "720p", "1080p", "4k":
			res = strings.ToLower(text)
		case "1280x720":
			res, ar = "720p", "16:9"
		case "720x1280":
			res, ar = "720p", "9:16"
		case "1920x1080":
			res, ar = "1080p", "16:9"
		case "1080x1920":
			res, ar = "1080p", "9:16"
		default:
			return fail(fmt.Errorf("unsupported Veo size"))
		}
		if _, exists := fields["resolution"]; exists && resolution != res {
			return fail(fmt.Errorf("size and resolution conflict"))
		}
		resolution = res
		if ar != "" {
			if value, exists := fields["ratio"]; exists && value != ar {
				return fail(fmt.Errorf("size and ratio conflict"))
			}
			if value, exists := fields["aspect_ratio"]; exists && value != ar {
				return fail(fmt.Errorf("size and aspect_ratio conflict"))
			}
			aspect = ar
		}
	}
	if resolution != "720p" && resolution != "1080p" && resolution != "4k" {
		return fail(fmt.Errorf("resolution must be 720p, 1080p or 4k"))
	}
	if resolution != "720p" && seconds != 8 {
		return fail(fmt.Errorf("1080p and 4k require 8 seconds"))
	}
	audio := true
	if value, exists := fields["generate_audio"]; exists {
		var valid bool
		audio, valid = value.(bool)
		if !valid {
			return fail(fmt.Errorf("generate_audio must be boolean"))
		}
	}
	if !official && !audio {
		return capability("silent generation requires the official Veo channel")
	}
	if count, exists := fields["sample_count"]; exists && count != float64(1) {
		return fail(fmt.Errorf("sample_count must be 1"))
	}
	for _, key := range []string{"raw", "nsfw_check", "enhance_prompt"} {
		if value, exists := fields[key]; exists {
			if _, ok := value.(bool); !ok {
				return fail(fmt.Errorf("%s must be boolean", key))
			}
		}
	}
	if fields["nsfw_check"] == true {
		return fail(fmt.Errorf("paid upstream moderation is not supported on this route"))
	}
	if fields["enhance_prompt"] == false {
		return fail(fmt.Errorf("enhance_prompt cannot be false"))
	}
	if seed, exists := fields["seed"]; exists {
		n, err := strconv.ParseUint(fmt.Sprint(seed), 10, 32)
		if err != nil {
			return fail(fmt.Errorf("seed must be an unsigned 32-bit integer"))
		}
		fields["seed"] = n
	}
	for _, key := range []string{"negative_prompt", "person_generation", "resize_mode", "input_reference", "first_frame_image", "last_frame_image", "generation_type"} {
		if value, exists := fields[key]; exists {
			text, ok := value.(string)
			if !ok || key != "negative_prompt" && strings.TrimSpace(text) == "" {
				return fail(fmt.Errorf("%s must be a string", key))
			}
			if key == "person_generation" && text != "allow_adult" && text != "disallow" {
				return fail(fmt.Errorf("person_generation must be allow_adult or disallow"))
			}
			if key == "resize_mode" && text != "pad" && text != "crop" {
				return fail(fmt.Errorf("resize_mode must be pad or crop"))
			}
		}
	}
	images := []string{}
	if values, exists := fields["image_urls"]; exists {
		list, ok := values.([]any)
		if !ok {
			return fail(fmt.Errorf("image_urls must be an array"))
		}
		for _, v := range list {
			text, ok := v.(string)
			if !ok {
				return fail(fmt.Errorf("image_urls must contain URLs"))
			}
			images = append(images, text)
		}
	}
	if first, ok := fields["input_reference"].(string); ok {
		if len(images) > 0 {
			return fail(fmt.Errorf("input_reference conflicts with image_urls"))
		}
		images = append(images, first)
	}
	if first, ok := fields["first_frame_image"].(string); ok {
		if len(images) > 0 {
			return fail(fmt.Errorf("first frame conflicts with image_urls"))
		}
		images = append(images, first)
	}
	if last, ok := fields["last_frame_image"].(string); ok {
		if len(images) != 1 {
			return fail(fmt.Errorf("last frame requires exactly one first frame"))
		}
		images = append(images, last)
	}
	if len(images) > 3 {
		return fail(fmt.Errorf("too many Veo reference images"))
	}
	for _, image := range images {
		u, err := url.ParseRequestURI(image)
		if err != nil || u.Host == "" || u.Scheme != "https" && u.Scheme != "http" {
			return fail(fmt.Errorf("Veo images must be public HTTP(S) URLs"))
		}
	}
	generation, _ := fields["generation_type"].(string)
	if generation != "" && generation != "frame" && generation != "reference" {
		return fail(fmt.Errorf("unsupported generation_type"))
	}
	if generation == "frame" && len(images) > 2 {
		return fail(fmt.Errorf("frame mode supports at most two images"))
	}
	if name == Standard && (generation == "reference" || len(images) == 3) {
		return fail(fmt.Errorf("Standard does not support reference generation"))
	}
	if len(images) == 3 && generation == "" {
		generation = "reference"
	}
	if generation == "reference" && (len(images) == 0 || aspect != "16:9") {
		return fail(fmt.Errorf("reference generation requires images and 16:9"))
	}
	if generation == "reference" && official {
		return capability("reference generation requires the Ext Fast channel")
	}
	remix := info.Action == constant.TaskActionRemix
	parentID := ""
	if remix {
		if official {
			return fail(fmt.Errorf("Veo remix requires an Ext origin task"))
		}
		parent, found, err := model.GetByTaskId(info.UserId, info.OriginTaskID)
		if err != nil || !found || parent == nil || parent.Status != model.TaskStatusSuccess || parent.Properties.OriginModelName != name {
			return fail(fmt.Errorf("remix requires your completed task with the same model"))
		}
		parentID = parent.GetUpstreamTaskID()
		if len(images) > 0 {
			return fail(fmt.Errorf("remix does not accept images"))
		}
	}
	for _, key := range []string{"size", "seconds", "ratio", "metadata", "input_reference", "first_frame_image", "last_frame_image", "image_urls", "generation_type"} {
		delete(fields, key)
	}
	fields["model"], fields["duration"], fields["resolution"], fields["aspect_ratio"] = upstream, seconds, resolution, aspect
	if official {
		fields["generate_audio"] = audio
		if len(images) > 0 {
			fields["first_frame_image"] = images[0]
		}
		if len(images) > 1 {
			fields["last_frame_image"] = images[1]
		}
		if resolution == "4k" {
			fields["resolution"] = "4K"
		}
		if _, exists := fields["raw"]; exists {
			return fail(fmt.Errorf("raw is only supported for remix"))
		}
	} else {
		for _, key := range []string{"negative_prompt", "seed", "person_generation", "resize_mode", "enhance_prompt"} {
			if _, exists := fields[key]; exists {
				return capability(key + " requires the official channel")
			}
		}
		delete(fields, "sample_count")
		delete(fields, "generate_audio")
		if len(images) > 0 {
			fields["image_urls"] = images
		}
		if generation != "" {
			fields["generation_type"] = generation
		}
		if !remix {
			if _, exists := fields["raw"]; exists {
				return fail(fmt.Errorf("raw is only supported for remix"))
			}
		}
	}
	if remix {
		delete(fields, "duration")
		delete(fields, "generation_type")
		c.Set("veo_remix_parent", parentID)
	}
	c.Set("veo_request", fields)
	c.Set("task_request", relaycommon.TaskSubmitReq{Model: name, Prompt: prompt, Duration: seconds, Seconds: strconv.Itoa(seconds), Size: resolution, Images: images, Metadata: map[string]any{"resolution": resolution, "generate_audio": audio, "aspect_ratio": aspect}})
	if !remix {
		info.Action = constant.TaskActionTextGenerate
		if len(images) > 0 {
			info.Action = constant.TaskActionGenerate
		}
	}
	return nil
}

func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}
	resolution, _ := req.Metadata["resolution"].(string)
	audio, _ := req.Metadata["generate_audio"].(bool)
	base := OfficialPrice(req.Model, "720p", true)
	return map[string]float64{"seconds": float64(req.Duration), "size": OfficialPrice(req.Model, resolution, audio) / base}
}
func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if info.Action == constant.TaskActionRemix {
		parent, found, err := model.GetByTaskId(info.UserId, info.OriginTaskID)
		if err != nil || !found {
			return "", fmt.Errorf("origin task not found")
		}
		return a.baseURL + "/v1/videos/" + url.PathEscape(parent.GetUpstreamTaskID()) + "/remix", nil
	}
	return a.baseURL + "/v1/videos/generations", nil
}
func (a *TaskAdaptor) BuildRequestHeader(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	req.Header.Set("Content-Type", "application/json")
	return nil
}
func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	body, ok := c.Get("veo_request")
	if !ok {
		return nil, fmt.Errorf("Veo request was not validated")
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
	if err != nil || !gjson.ValidBytes(raw) || gjson.GetBytes(raw, "code").Int() != 200 || id == "" {
		return "", nil, service.TaskErrorWrapper(fmt.Errorf("invalid Veo submission response"), "invalid_response", http.StatusBadGateway)
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
		return nil, fmt.Errorf("invalid Veo task response")
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
			return nil, fmt.Errorf("completed Veo task has no video URL")
		}
	case "failed", "failure", "error", "canceled", "cancelled":
		result.Status = model.TaskStatusFailure
		result.Progress = "100%"
		result.Reason = data.Get("error.message").String()
		if result.Reason == "" {
			result.Reason = "Veo generation failed"
		}
	default:
		return nil, fmt.Errorf("unrecognized Veo task status")
	}
	return result, nil
}
func (a *TaskAdaptor) ConvertToOpenAIVideo(task *model.Task) ([]byte, error) {
	out := map[string]any{"id": task.TaskID, "task_id": task.TaskID, "object": "video", "model": task.Properties.OriginModelName, "status": task.Status.ToVideoStatus(), "progress": task.Progress, "created_at": task.CreatedAt}
	if task.Status == model.TaskStatusSuccess {
		out["url"] = taskcommon.BuildProxyURL(task.TaskID)
	}
	if task.Status == model.TaskStatusFailure {
		out["error"] = map[string]string{"code": "generation_failed", "message": service.PublicTaskFailure(task)}
	}
	return common.Marshal(out)
}
func (a *TaskAdaptor) GetModelList() []string { return []string{Standard, Fast} }
func (a *TaskAdaptor) GetChannelName() string { return "APIMart Veo 3.1" }
