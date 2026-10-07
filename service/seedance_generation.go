package service

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
)

var SeedanceDraftInheritedFields = []string{"prompt", "image_urls", "image_with_roles", "video_urls", "audio_urls", "duration", "size", "aspect_ratio", "ratio", "seed", "generate_audio", "audio", "omni_reference_task_type", "generation_type", "camerafixed", "web_search", "tools", "seconds", "images", "image", "input_reference", "first_frame_image", "last_frame_image", "video_list", "negative_prompt", "metadata"}

// NormalizeSeedance25Generation runs before routing/pre-consumption. Cached
// public fields are reused by the adapter; private identifiers are resolved only
// when building the outbound request.
func NormalizeSeedance25Generation(c *gin.Context, input map[string]any) (map[string]any, error) {
	if cached, ok := c.Get("seedance25_normalized_request"); ok {
		return cached.(map[string]any), nil
	}
	f := map[string]any{}
	for k, v := range input {
		f[k] = v
	}
	if _, hasID := input["draft_task_id"]; hasID {
		if _, hasMetadata := input["metadata"]; hasMetadata {
			return nil, fmt.Errorf("metadata must not be supplied with draft_task_id")
		}
	}
	if md, exists := f["metadata"]; exists {
		var m map[string]any
		switch v := md.(type) {
		case map[string]any:
			m = v
		case string:
			if common.Unmarshal([]byte(v), &m) != nil {
				return nil, fmt.Errorf("metadata must be a JSON object")
			}
		default:
			return nil, fmt.Errorf("metadata must be a JSON object")
		}
		for k, v := range m {
			if _, ok := f[k]; !ok {
				f[k] = v
			}
		}
		delete(f, "metadata")
	}
	text := func(k string) (string, error) {
		v, ok := f[k]
		if !ok {
			return "", nil
		}
		s, ok := v.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return "", fmt.Errorf("%s must be a non-empty string", k)
		}
		return strings.TrimSpace(s), nil
	}
	for _, k := range []string{"draft", "return_last_frame", "watermark", "generate_audio", "audio", "nsfw_check", "camerafixed", "web_search"} {
		if v, exists := f[k]; exists {
			if _, ok := v.(bool); !ok {
				return nil, fmt.Errorf("%s must be a boolean", k)
			}
		}
	}
	draft, _ := f["draft"].(bool)
	id, err := text("draft_task_id")
	if err != nil {
		return nil, err
	}
	if draft && id != "" {
		return nil, fmt.Errorf("draft and draft_task_id cannot be used together")
	}
	if draft || id != "" {
		if _, ok := f["service_tier"]; ok {
			return nil, fmt.Errorf("service_tier is not supported for draft tasks")
		}
	}
	if id != "" {
		allowed := map[string]bool{"model": true, "draft_task_id": true, "resolution": true, "output_format": true, "return_last_frame": true, "watermark": true, "draft": true}
		for _, k := range SeedanceDraftInheritedFields {
			if _, exists := f[k]; exists {
				return nil, fmt.Errorf("%s must not be supplied with draft_task_id", k)
			}
		}
		for k := range f {
			if !allowed[k] {
				return nil, fmt.Errorf("%s cannot be changed when using draft_task_id", k)
			}
		}
		task, found, e := model.GetByTaskId(c.GetInt("id"), id)
		if e != nil || !found || task == nil {
			return nil, fmt.Errorf("draft_task_id was not found in your account")
		}
		if task.Properties.OriginModelName != "seedance-2.5" {
			return nil, fmt.Errorf("draft_task_id model does not match")
		}
		if task.PrivateData.SeedanceRequest["draft"] != true {
			return nil, fmt.Errorf("draft_task_id is not a draft task")
		}
		if task.Status != model.TaskStatusSuccess {
			return nil, fmt.Errorf("draft_task_id has not completed successfully")
		}
		if task.CreatedAt <= 0 || time.Now().Unix()-task.CreatedAt >= 7*86400 {
			return nil, fmt.Errorf("draft_task_id has expired; drafts are valid for 7 days")
		}
		if task.GetUpstreamTaskID() == "" || task.PrivateData.Key == "" {
			return nil, fmt.Errorf("Draft is temporarily unavailable; retry with the same draft_task_id later")
		}
		ch, e := model.GetChannelById(task.ChannelId, true)
		if e != nil || !seedanceChannelAllowed(c, ch, "seedance-2.5") {
			return nil, fmt.Errorf("Draft is temporarily unavailable; retry with the same draft_task_id later")
		}
		fingerprint := SeedanceKeyFingerprint(task.PrivateData.Key)
		if _, _, e := SeedanceResourceKey(ch, fingerprint); e != nil {
			return nil, fmt.Errorf("Draft is temporarily unavailable; retry with the same draft_task_id later")
		}
		c.Set("seedance_draft_task", task)
		c.Set("seedance_draft_key_fingerprint", fingerprint)
		if audio, ok := task.PrivateData.SeedanceRequest["generate_audio"].(bool); ok {
			f["generate_audio"] = audio
		}
		f["duration"] = task.PrivateData.SeedanceRequest["duration"]
		if f["duration"] == nil {
			f["duration"] = 5
		}
	} else {
		// Preserve APIMaster's existing metadata and OpenAI image aliases.

		if _, ok := f["image_urls"]; !ok {
			if images, ok := f["images"]; ok {
				f["image_urls"] = images
			} else {
				for _, k := range []string{"input_reference", "image"} {
					if u, ok := f[k].(string); ok {
						f["image_urls"] = []any{u}
						break
					}
				}
			}
		}
		if _, ok := f["image_with_roles"]; !ok {
			roles := []any{}
			for _, p := range []struct{ key, role string }{{"first_frame_image", "first_frame"}, {"last_frame_image", "last_frame"}} {
				if u, ok := f[p.key].(string); ok && u != "" {
					roles = append(roles, map[string]any{"url": u, "role": p.role})
				}
			}
			if len(roles) > 0 {
				f["image_with_roles"] = roles
			}
		}
		for _, k := range []string{"first_frame_image", "last_frame_image", "images", "image", "input_reference"} {
			delete(f, k)
		}
	}
	resolution, e := text("resolution")
	if e != nil {
		return nil, e
	}
	aspect, e := text("aspect_ratio")
	if e != nil {
		return nil, e
	}
	ratio, e := text("ratio")
	if e != nil {
		return nil, e
	}
	if aspect != "" && ratio != "" && aspect != ratio {
		return nil, fmt.Errorf("ratio and aspect_ratio conflict")
	}
	if aspect == "" {
		aspect = ratio
	}
	size, e := text("size")
	if e != nil {
		return nil, e
	}
	if size != "" {
		sr, sa := "", ""
		switch strings.ToLower(size) {
		case "480p", "720p", "1080p":
			sr = strings.ToLower(size)
		case "1280x720":
			sr, sa = "720p", "16:9"
		case "720x1280":
			sr, sa = "720p", "9:16"
		case "1920x1080":
			sr, sa = "1080p", "16:9"
		case "1080x1920":
			sr, sa = "1080p", "9:16"
		case "portrait":
			sa = "9:16"
		case "landscape":
			sa = "16:9"
		case "16:9", "4:3", "1:1", "3:4", "9:16", "21:9", "adaptive":
			sa = size
		default:
			return nil, fmt.Errorf("unsupported video size %q", size)
		}
		if resolution != "" && sr != "" && !strings.EqualFold(resolution, sr) {
			return nil, fmt.Errorf("size and resolution conflict")
		}
		if aspect != "" && sa != "" && aspect != sa {
			return nil, fmt.Errorf("size and aspect_ratio conflict")
		}
		if resolution == "" {
			resolution = sr
		}
		if aspect == "" {
			aspect = sa
		}
	}
	if resolution == "" {
		switch {
		case draft:
			resolution = "480p"
		case id != "":
			resolution = "1080p"
		default:
			resolution = "720p"
		}
	}
	resolution = strings.ToLower(resolution)
	if draft && resolution != "480p" {
		return nil, fmt.Errorf("draft resolution must be 480p")
	}
	if id != "" && resolution != "1080p" {
		return nil, fmt.Errorf("draft_task_id resolution must be 1080p")
	}
	if resolution != "480p" && resolution != "720p" && resolution != "1080p" {
		return nil, fmt.Errorf("unsupported seedance-2.5 resolution %q", resolution)
	}
	if aspect == "" {
		aspect = "adaptive"
	}
	switch aspect {
	case "16:9", "4:3", "1:1", "3:4", "9:16", "21:9", "adaptive":
	default:
		return nil, fmt.Errorf("unsupported aspect_ratio %q", aspect)
	}
	parse := func(v any) (int, error) {
		n, e := strconv.Atoi(fmt.Sprint(v))
		if e != nil {
			return 0, fmt.Errorf("duration and seconds must be integers")
		}
		return n, nil
	}
	if s, ok := f["seconds"]; ok {
		n, e := parse(s)
		if e != nil {
			return nil, e
		}
		if d, exists := f["duration"]; exists {
			dn, e := parse(d)
			if e != nil {
				return nil, e
			}
			if n != dn {
				return nil, fmt.Errorf("duration and seconds conflict")
			}
		}
		f["duration"] = n
	}
	kind, e := text("omni_reference_task_type")
	if e != nil {
		return nil, e
	}
	if kind == "" {
		kind = "auto"
	}
	switch kind {
	case "auto", "reference", "edit", "extend":
	default:
		return nil, fmt.Errorf("omni_reference_task_type must be auto, reference, edit, or extend")
	}
	duration := 5
	if kind == "edit" {
		duration = -1
	}
	if v, exists := f["duration"]; exists {
		duration, e = parse(v)
		if e != nil {
			return nil, e
		}
	}
	if duration != -1 && (duration < 4 || duration > 30) {
		return nil, fmt.Errorf("seedance-2.5 duration must be -1 or an integer from 4 to 30")
	}
	if kind == "edit" || kind == "extend" {
		if seedanceInputCount(f["video_urls"]) < 1 {
			return nil, fmt.Errorf("%s requires at least one video_urls entry", kind)
		}
		if aspect != "adaptive" {
			return nil, fmt.Errorf("%s aspect_ratio must be adaptive", kind)
		}
		if kind == "edit" && duration != -1 {
			return nil, fmt.Errorf("edit duration must be -1")
		}
	}
	for _, slot := range []struct {
		field   string
		maximum int
	}{{"image_urls", 30}, {"audio_urls", 10}, {"video_urls", 10}} {
		if value, exists := f[slot.field]; exists {
			items, ok := value.([]any)
			if !ok {
				return nil, fmt.Errorf("%s must be an array of strings", slot.field)
			}
			if len(items) > slot.maximum {
				return nil, fmt.Errorf("%s supports at most %d entries", slot.field, slot.maximum)
			}
			for _, item := range items {
				if u, ok := item.(string); !ok || strings.TrimSpace(u) == "" {
					return nil, fmt.Errorf("%s must contain non-empty strings", slot.field)
				}
			}
		}
	}
	if value, exists := f["image_with_roles"]; exists {
		roles, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("image_with_roles must be an array")
		}
		if len(roles)+seedanceInputCount(f["image_urls"]) > 30 {
			return nil, fmt.Errorf("Image references must not exceed 30 entries")
		}
		first, last := 0, 0
		for _, value := range roles {
			role, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("image_with_roles entries must be objects")
			}
			if u, ok := role["url"].(string); !ok || strings.TrimSpace(u) == "" {
				return nil, fmt.Errorf("image_with_roles entries require a non-empty url")
			}
			switch role["role"] {
			case "first_frame":
				first++
			case "last_frame":
				last++
			case "reference_image":
			default:
				return nil, fmt.Errorf("image_with_roles role must be first_frame, last_frame, or reference_image")
			}
		}
		if first > 1 || last > 1 {
			return nil, fmt.Errorf("Use at most one first_frame and one last_frame")
		}
		if seedanceInputCount(f["video_urls"]) == 0 && seedanceInputCount(f["audio_urls"]) == 0 {
			if last > 0 && first == 0 {
				return nil, fmt.Errorf("last_frame requires first_frame")
			}
			if (first > 0 || last > 0) && aspect != "adaptive" {
				return nil, fmt.Errorf("first/last frame aspect_ratio must be adaptive")
			}
		}
	}

	if a, ok := f["audio"]; ok {
		if g, exists := f["generate_audio"]; exists && g != a {
			return nil, fmt.Errorf("audio and generate_audio conflict")
		}
		f["generate_audio"] = a
	}
	delete(f, "audio")
	for k, v := range map[string]any{"generate_audio": true, "watermark": false, "output_format": "mp4", "nsfw_check": false, "return_last_frame": false} {
		if _, exists := f[k]; !exists {
			f[k] = v
		}
	}
	if f["output_format"] != "mp4" && f["output_format"] != "mov" {
		return nil, fmt.Errorf("output_format must be mp4 or mov")
	}
	f["resolution"], f["aspect_ratio"], f["duration"] = resolution, aspect, duration
	if id == "" {
		f["omni_reference_task_type"] = kind
	}
	delete(f, "ratio")
	delete(f, "size")
	delete(f, "seconds")
	c.Set("seedance25_normalized_request", f)
	return f, nil
}

func seedanceInputCount(value any) int {
	switch v := value.(type) {
	case []any:
		return len(v)
	case []string:
		return len(v)
	}
	return 0
}

func ValidateSeedanceVideoInputs(c *gin.Context, fields map[string]any) error {
	if _, upgrade := c.Get("seedance_draft_task"); upgrade {
		return nil
	}
	if fields["prompt"] == nil || strings.TrimSpace(fmt.Sprint(fields["prompt"])) == "" {
		return fmt.Errorf("prompt is required")
	}
	value, exists := fields["video_urls"]
	if !exists {
		return nil
	}
	urls, ok := value.([]any)
	if !ok {
		return fmt.Errorf("video_urls must be an array of strings")
	}
	maximum, count := 30, 10
	if name, _ := fields["model"].(string); IsSeedance20Variant(name) || name == "seedance-2.0" {
		maximum, count = 15, 3
	}
	if len(urls) > count {
		return fmt.Errorf("video_urls supports at most %d videos", count)
	}
	total := 0
	measurements := make([]model.SeedanceInputVideoMeasurement, 0, len(urls))
	for i, v := range urls {
		u, ok := v.(string)
		if !ok || u == "" {
			return fmt.Errorf("video_urls[%d] must be a non-empty string", i)
		}
		seconds := 0
		var measured *float64
		measurementSource := "apimaster_probe"
		mediaID := seedanceProtectedMediaID(c.GetInt("id"), u)
		var libraryAsset *model.SeedanceResource
		if strings.HasPrefix(u, "asset://") {
			asset, e := seedanceOwnedAsset(c, strings.TrimPrefix(u, "asset://"))
			if e != nil {
				return e
			}
			if asset.AssetType != "Video" {
				return fmt.Errorf("video_urls[%d] must reference a video asset", i)
			}
			libraryAsset = asset
			seconds = asset.DurationSeconds
			mediaID = asset.ID
			measurementSource = "apimaster_asset_cache"
			if asset.MeasuredDurationSeconds > 0 {
				raw := asset.MeasuredDurationSeconds
				measured = &raw
			}
			u = asset.SourceURL
		}
		if seconds <= 0 {
			setting := system_setting.GetFetchSetting()
			if common.ValidateURLWithFetchSetting(u, setting.EnableSSRFProtection, setting.AllowPrivateIp, setting.DomainFilterMode, setting.IpFilterMode, setting.DomainList, setting.IpList, setting.AllowedPorts, setting.ApplyIPFilterForDomain) != nil {
				return fmt.Errorf("video_urls[%d] is not an accessible video URL", i)
			}
			var e error
			raw, probeErr := ProbeRemoteVideoDuration(c.Request.Context(), u)
			e = probeErr
			seconds = int(math.Ceil(raw))
			measured = &raw
			if e != nil {
				return fmt.Errorf("Unable to verify video_urls[%d] duration; provide an accessible MP4 or MOV video", i)
			}

			if libraryAsset != nil {
				_ = model.DB.Model(libraryAsset).Updates(map[string]any{"duration_seconds": seconds, "measured_duration_seconds": *measured}).Error
			}
		}
		minimum := 2
		if fields["omni_reference_task_type"] == "edit" {
			minimum = 4
		}
		if seconds < minimum || seconds > maximum {
			return fmt.Errorf("video_urls[%d] duration must be from %d to %d seconds", i, minimum, maximum)
		}
		total += seconds
		measurements = append(measurements, model.SeedanceInputVideoMeasurement{Index: i, MediaID: mediaID, MeasuredSeconds: measured, BillableSeconds: seconds, DurationSource: measurementSource})
	}
	if total > maximum {
		return fmt.Errorf("video_urls total duration must not exceed %d seconds", maximum)
	}
	c.Set("seedance_video_input_seconds", total)
	c.Set("seedance_video_input_measurements", measurements)
	return nil
}

// An upgrade inherits content at the provider. Sending derived billing fields
// again would violate the provider's upgrade contract.
func ResolveSeedanceDraftRequest(c *gin.Context, fields map[string]any) map[string]any {
	taskValue, ok := c.Get("seedance_draft_task")
	if !ok {
		return fields
	}
	task := taskValue.(*model.Task)
	out := map[string]any{}
	for k, v := range fields {
		out[k] = v
	}
	for _, k := range SeedanceDraftInheritedFields {
		delete(out, k)
	}
	delete(out, "nsfw_check")
	delete(out, "draft")
	out["draft_task_id"] = task.GetUpstreamTaskID()
	return out
}
