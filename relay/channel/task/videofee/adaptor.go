package videofee

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
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/task/apimartvideo"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// TaskAdaptor retains APIMaster's public validation and frozen per-second
// billing. Only the VideoFee transport and response envelope differ.
type TaskAdaptor struct {
	apimartvideo.TaskAdaptor
	baseURL string
	apiKey  string
}

func IsModel(name string) bool { return name == "seedance-2.0" || name == "seedance-2.5" }

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.TaskAdaptor.Init(info)
	a.baseURL = strings.TrimRight(info.ChannelBaseUrl, "/")
	a.apiKey = info.ApiKey
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	if !strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
		return service.TaskErrorWrapperLocal(fmt.Errorf("this video route requires JSON; use public media URLs or your media library assets"), "invalid_request", http.StatusBadRequest)
	}
	if err := a.TaskAdaptor.ValidateRequestAndSetAction(c, info); err != nil {
		return err
	}
	raw, err := apimartvideo.NormalizeGenerationJSON(c)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	var fields map[string]any
	if err = common.Unmarshal(raw, &fields); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	// Validate the conversion before wallet reservation, preserving explicit
	// false/zero options through maps rather than omitempty scalar fields.
	if _, err = contentPayload(fields); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	return nil
}

func contentPayload(fields map[string]any) (map[string]any, error) {
	out := make(map[string]any)
	skip := map[string]bool{"model": true, "prompt": true, "image_urls": true, "image_with_roles": true, "video_urls": true, "audio_urls": true, "images": true, "image": true, "input_reference": true, "aspect_ratio": true, "size": true, "seconds": true, "metadata": true, "input_seconds": true, "reference_seconds": true, "idempotency_key": true, "content": true, "nsfw_check": true}
	for key, value := range fields {
		if !skip[key] {
			out[key] = value
		}
	}
	out["ratio"] = fields["aspect_ratio"]
	if camera, ok := fields["camerafixed"]; ok {
		out["camera_fixed"] = camera
		delete(out, "camerafixed")
	}
	if audio, ok := fields["audio"]; ok {
		if _, exists := out["generate_audio"]; !exists {
			out["generate_audio"] = audio
		}
		delete(out, "audio")
	}
	if callback, ok := fields["webhook"].(string); ok && callback != "" {
		if _, exists := out["callback_url"]; !exists {
			out["callback_url"] = callback
		}
		delete(out, "webhook")
	}
	content := []any{}
	if raw, ok := fields["content"]; ok {
		for _, key := range []string{"image_urls", "image_with_roles", "video_urls", "audio_urls"} {
			if list, exists := fields[key]; exists {
				encoded, _ := common.Marshal(list)
				var items []any
				if common.Unmarshal(encoded, &items) != nil || len(items) > 0 {
					return nil, fmt.Errorf("use content or public media fields, not both")
				}
			}
		}
		items, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("content must be an array")
		}
		for _, value := range items {
			item, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("content entries must be objects")
			}
			if item["type"] != "text" {
				clone := map[string]any{}
				for k, v := range item {
					clone[k] = v
				}
				content = append(content, clone)
			}
		}
	}
	add := func(kind, role, source string) {
		content = append(content, map[string]any{"type": kind + "_url", "role": role, kind + "_url": map[string]any{"url": source}})
	}
	for _, kind := range []string{"image", "video", "audio"} {
		if raw, exists := fields[kind+"_urls"]; exists {
			data, err := common.Marshal(raw)
			if err != nil {
				return nil, err
			}
			var list []string
			if err = common.Unmarshal(data, &list); err != nil {
				return nil, fmt.Errorf("%s_urls must be an array of strings", kind)
			}
			for _, source := range list {
				if strings.TrimSpace(source) == "" {
					return nil, fmt.Errorf("%s_urls contains an empty URL", kind)
				}
				add(kind, "reference_"+kind, source)
			}
		}
	}
	if raw, exists := fields["image_with_roles"]; exists {
		items, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("image_with_roles must be an array")
		}
		for _, value := range items {
			item, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("image_with_roles entries must be objects")
			}
			source, _ := item["url"].(string)
			role, _ := item["role"].(string)
			if strings.TrimSpace(source) == "" || (role != "first_frame" && role != "last_frame" && role != "reference_image") {
				return nil, fmt.Errorf("image_with_roles requires a URL and valid image role")
			}
			add("image", role, source)
		}
	}
	for _, value := range content {
		item := value.(map[string]any)
		kind, _ := item["type"].(string)
		if kind != "image_url" && kind != "video_url" && kind != "audio_url" {
			return nil, fmt.Errorf("unsupported content type")
		}
		if role, _ := item["role"].(string); role == "" {
			item["role"] = "reference_" + strings.TrimSuffix(kind, "_url")
		}
	}
	if prompt, ok := fields["prompt"].(string); ok && strings.TrimSpace(prompt) != "" {
		content = append([]any{map[string]any{"type": "text", "text": prompt}}, content...)
	}
	out["content"] = content
	return out, nil
}

func (a *TaskAdaptor) BuildRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	return a.baseURL + "/v1/video/generations", nil
}
func (a *TaskAdaptor) BuildRequestHeader(_ *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	// Bind upstream retries to the gateway task, never to a caller key that could
	// accidentally merge independent gateway reservations into one upstream task.
	if info.PublicTaskID != "" {
		req.Header.Set("Idempotency-Key", "apimaster-"+info.PublicTaskID)
	}
	return nil
}
func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	raw, err := apimartvideo.NormalizeGenerationJSON(c)
	if err != nil {
		return nil, err
	}
	var fields map[string]any
	if err = common.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	fields, err = service.ResolveSeedanceAssetReferences(c, fields, info.ChannelId, a.apiKey)
	if err != nil {
		return nil, err
	}
	fields = service.ResolveSeedanceDraftRequest(c, fields)
	out, err := contentPayload(fields)
	if err != nil {
		return nil, err
	}
	if _, upgrade := fields["draft_task_id"]; upgrade {
		delete(out, "content")
		delete(out, "ratio")
	}
	out["model"] = info.UpstreamModelName
	if _, ok := out["duration"]; !ok {
		req, e := relaycommon.GetTaskRequest(c)
		if e != nil {
			return nil, e
		}
		out["duration"] = req.Duration
	}
	data, err := common.Marshal(out)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}
func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, body io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, body)
}
func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (string, []byte, *dto.TaskError) {
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, service.TaskErrorWrapper(err, "read_response_body_failed", 502)
	}
	var body map[string]any
	if err = common.Unmarshal(raw, &body); err != nil {
		return "", nil, service.TaskErrorWrapper(err, "invalid_response", 502)
	}
	id, _ := body["id"].(string)
	if id == "" {
		id, _ = body["task_id"].(string)
	}
	if id == "" {
		return "", nil, service.TaskErrorWrapper(fmt.Errorf("upstream did not return a task ID"), "invalid_response", 502)
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
	// Provider billing is procurement metadata, never the customer receipt.
	return id, raw, nil
}
func (a *TaskAdaptor) FetchTask(baseURL, key string, body map[string]any, proxy string) (*http.Response, error) {
	id, ok := body["task_id"].(string)
	if !ok || id == "" {
		return nil, fmt.Errorf("invalid task ID")
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+"/v1/video/generations/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, err
	}
	return client.Do(req)
}
func (a *TaskAdaptor) ParseTaskResult(raw []byte) (*relaycommon.TaskInfo, error) {
	if !gjson.ValidBytes(raw) {
		return nil, fmt.Errorf("invalid VideoFee task response")
	}
	data := gjson.ParseBytes(raw)
	if nested := data.Get("data"); nested.IsObject() {
		data = nested
	}
	result := &relaycommon.TaskInfo{TaskID: data.Get("task_id").String(), Progress: data.Get("progress").String()}
	if result.TaskID == "" {
		result.TaskID = data.Get("id").String()
	}
	switch strings.ToUpper(data.Get("status").String()) {
	case "QUEUED", "PENDING", "SUBMITTED":
		result.Status = model.TaskStatusQueued
	case "IN_PROGRESS", "PROCESSING", "RUNNING":
		result.Status = model.TaskStatusInProgress
	case "SUCCESS", "SUCCEEDED", "COMPLETED":
		result.Status = model.TaskStatusSuccess
		result.Progress = "100%"
		for _, path := range []string{"result_url", "data.url", "video_url", "content.video_url", "url"} {
			if u := data.Get(path).String(); service.IsValidMediaResultURL(u) {
				result.Url = u
				break
			}
		}
		if result.Url == "" {
			return nil, fmt.Errorf("completed upstream task has no valid result URL")
		}
		for _, path := range []string{"duration", "output_duration", "data.duration", "data.output_duration"} {
			if seconds := data.Get(path).Float(); seconds > 0 {
				result.BillableSeconds = int(seconds + 0.5)
				break
			}
		}
	case "FAILURE", "FAILED", "CANCELLED", "CANCELED":
		result.Status = model.TaskStatusFailure
		result.Progress = "100%"
		result.Reason = data.Get("fail_reason").String()
		if result.Reason == "" {
			result.Reason = data.Get("error.message").String()
		}
		if result.Reason == "" {
			result.Reason = "Video generation failed"
		}
	default:
		return nil, fmt.Errorf("unrecognized VideoFee task status")
	}
	if result.Progress == "" {
		result.Progress = "0%"
	} else if !strings.HasSuffix(result.Progress, "%") {
		if n, e := strconv.Atoi(result.Progress); e == nil {
			result.Progress = fmt.Sprintf("%d%%", min(100, max(0, n)))
		}
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
func (a *TaskAdaptor) GetModelList() []string { return []string{"seedance-2.0", "seedance-2.5"} }
func (a *TaskAdaptor) GetChannelName() string { return "VideoFee" }
