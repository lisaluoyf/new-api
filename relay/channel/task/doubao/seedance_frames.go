package doubao

import (
	"fmt"
	"net/url"
	"strings"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

// Keep the public first/last-frame contract when an Ark-compatible channel is
// selected. Unknown public fields must never silently become text-only tasks.
func normalizeSeedanceFrameAliases(fields, metadata map[string]any) error {
	value, exists := fields["image_with_roles"]
	if !exists {
		value, exists = metadata["image_with_roles"]
	}
	if !exists {
		aliases := []any{}
		for _, alias := range []struct{ key, role string }{{"first_frame_image", "first_frame"}, {"last_frame_image", "last_frame"}} {
			if value, ok := fields[alias.key]; ok {
				aliases = append(aliases, map[string]any{"url": value, "role": alias.role})
			}
		}
		if len(aliases) == 0 {
			return nil
		}
		value = aliases
	}
	entries, ok := value.([]any)
	if !ok {
		return fmt.Errorf("image_with_roles must be an array")
	}
	limit := 9
	if fields["model"] == "seedance-2.5" {
		limit = 30
	}
	if len(entries) > limit {
		return fmt.Errorf("image_with_roles supports at most %d images", limit)
	}
	content, _ := metadata["content"].([]any)
	roles := map[string]int{}
	for _, value := range entries {
		entry, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("image_with_roles entries must be objects")
		}
		u, ok := entry["url"].(string)
		if !ok || strings.TrimSpace(u) == "" {
			return fmt.Errorf("image_with_roles entries require a non-empty url")
		}
		role, _ := entry["role"].(string)
		if role != "first_frame" && role != "last_frame" && role != "reference_image" {
			return fmt.Errorf("image_with_roles role must be first_frame, last_frame, or reference_image")
		}
		roles[role]++
		content = append(content, map[string]any{"type": "image_url", "image_url": map[string]string{"url": u}, "role": role})
	}
	if roles["first_frame"] > 1 || roles["last_frame"] > 1 {
		return fmt.Errorf("Use at most one first_frame and one last_frame")
	}
	if roles["last_frame"] > 0 && roles["first_frame"] == 0 {
		return fmt.Errorf("last_frame requires first_frame")
	}
	if (roles["first_frame"] > 0 || roles["last_frame"] > 0) && metadata["ratio"] != "adaptive" {
		return fmt.Errorf("first/last frame aspect_ratio must be adaptive")
	}
	metadata["content"] = content
	return nil
}

// Validation runs before task model mapping and pre-consumption. Resolve the
// mapping without mutating RelayInfo or applying this provider's rules globally.
func (a *TaskAdaptor) tencentSeedanceModel(c *gin.Context, info *relaycommon.RelayInfo, model string) string {
	base, err := url.Parse(a.baseURL)
	if err != nil || !strings.EqualFold(base.Hostname(), "yestoken.io") {
		return ""
	}
	mapping := c.GetString("model_mapping")
	if mapped := service.ModelMappingTarget(&mapping, model); mapped != "" {
		model = mapped
	} else if info != nil && info.ChannelMeta != nil && info.IsModelMapped {
		model = info.UpstreamModelName
	}
	if model == "tencent-seedance-1-5-pro" || model == "tencent-seedance-1-0-pro" {
		return model
	}
	return ""
}

func (a *TaskAdaptor) normalizeTencentSeedanceFrames(c *gin.Context, info *relaycommon.RelayInfo, req *relaycommon.TaskSubmitReq, body *requestPayload) error {
	model := a.tencentSeedanceModel(c, info, req.Model)
	if model == "" {
		return nil
	}
	var frames []int
	for i, item := range body.Content {
		// Reference and multimodal generation have different ratio contracts.
		if item.Type == "video_url" || item.Type == "audio_url" || (item.Type == "image_url" && item.Role != "" && item.Role != "first_frame" && item.Role != "last_frame") {
			return nil
		}
		if item.Type == "image_url" {
			frames = append(frames, i)
		}
	}
	if len(frames) == 0 {
		return nil
	}
	if len(frames) > 2 {
		return fmt.Errorf("first/last-frame generation accepts at most two images")
	}
	roles := map[string]bool{}
	for _, i := range frames {
		role := body.Content[i].Role
		if role != "" {
			if roles[role] {
				return fmt.Errorf("duplicate image role: %s", role)
			}
			roles[role] = true
		}
	}
	for _, i := range frames {
		item := &body.Content[i]
		if item.Role == "" {
			item.Role = "first_frame"
			if roles[item.Role] {
				item.Role = "last_frame"
			}
			roles[item.Role] = true
		}
	}
	if !roles["first_frame"] {
		return fmt.Errorf("last_frame requires a first_frame image")
	}
	for _, i := range frames {
		item := body.Content[i]
		if item.ImageURL == nil || strings.TrimSpace(item.ImageURL.URL) == "" {
			return fmt.Errorf("%s requires an image URL", item.Role)
		}
		source := types.NewFileSourceFromData(item.ImageURL.URL, "")
		config, _, err := service.GetImageConfig(c, source)
		if err != nil {
			// Do not echo signed URLs or inline image contents in public errors.
			return fmt.Errorf("cannot read %s image; provide a reachable, supported image", item.Role)
		}
		if cache := source.GetCache(); cache != nil && cache.Size >= 30*1024*1024 {
			return fmt.Errorf("%s image must be smaller than 30 MB", item.Role)
		}
		if err := validateSeedanceFrameSize(config.Width, config.Height); err != nil {
			return fmt.Errorf("%s: %w", item.Role, err)
		}
	}
	// Omission/empty ratio still fails on this provider. 1.5 requires adaptive
	// even when a fixed ratio matches the input; 1.0 supports explicit ratios.
	if model == "tencent-seedance-1-5-pro" || strings.TrimSpace(body.Ratio) == "" {
		req.Metadata["ratio"] = "adaptive"
	}
	req.Metadata["content"] = body.Content
	// Images have now been materialized into content, including their roles.
	req.Images = nil
	return nil
}

func validateSeedanceFrameSize(width, height int) error {
	if width < 300 || height < 300 || width > 6000 || height > 6000 {
		return fmt.Errorf("image dimensions must each be 300-6000 pixels (got %dx%d)", width, height)
	}
	if width*5 < height*2 || width*2 > height*5 {
		return fmt.Errorf("image width/height ratio must be 0.4-2.5 (got %dx%d)", width, height)
	}
	return nil
}
