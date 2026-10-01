package apimartvideo

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// Both public generation aliases use the same model-aware JSON contract.
func isGenerationJSON(c *gin.Context) bool {
	return strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") &&
		(strings.HasSuffix(c.Request.URL.Path, "/video/generations") || strings.HasSuffix(c.Request.URL.Path, "/videos/generations"))
}

func normalizedGenerationJSON(c *gin.Context) ([]byte, error) {
	if cached, ok := c.Get("apimart_normalized_request"); ok {
		return common.Marshal(cached)
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, err
	}
	raw, err := storage.Bytes()
	if err != nil {
		return nil, err
	}
	var fields map[string]interface{}
	if err := common.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, fmt.Errorf("request must be a JSON object")
	}
	if fields["model"] == ModelSeedance25 {
		normalized, err := service.NormalizeSeedance25Generation(c, fields)
		if err != nil {
			return nil, err
		}
		return common.Marshal(normalized)
	}
	requested := map[string]interface{}{}
	for _, key := range []string{"resolution", "size", "ratio", "aspect_ratio", "duration", "seconds"} {
		if value, ok := fields[key]; ok {
			requested[key] = value
		}
	}
	var md map[string]interface{}
	if value, ok := fields["metadata"]; ok {
		switch v := value.(type) {
		case map[string]interface{}:
			md = v
		case string:
			if err := common.Unmarshal([]byte(v), &md); err != nil {
				return nil, fmt.Errorf("metadata must be a JSON object")
			}
		case nil:
		default:
			return nil, fmt.Errorf("metadata must be a JSON object")
		}
	}
	for _, key := range []string{"resolution", "aspect_ratio", "ratio", "duration", "seconds", "image_urls", "video_urls", "audio_urls", "video_list", "audio", "mode"} {
		if _, exists := fields[key]; !exists && md != nil {
			if value, ok := md[key]; ok {
				fields[key] = value
				requested[key] = value
			}
		}
	}
	text := func(key string) (string, error) {
		v, exists := fields[key]
		if !exists {
			return "", nil
		}
		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("%s must be a string", key)
		}
		if strings.TrimSpace(s) == "" {
			return "", fmt.Errorf("%s must not be empty", key)
		}
		return strings.TrimSpace(s), nil
	}
	ratio, err := text("ratio")
	if err != nil {
		return nil, err
	}
	aspect, err := text("aspect_ratio")
	if err != nil {
		return nil, err
	}
	if ratio != "" && aspect != "" && ratio != aspect {
		return nil, fmt.Errorf("ratio and aspect_ratio conflict")
	}
	if aspect == "" {
		aspect = ratio
	}
	resolution, err := text("resolution")
	if err != nil {
		return nil, err
	}
	size, err := text("size")
	if err != nil {
		return nil, err
	}
	if size != "" {
		var sizeResolution, sizeAspect string
		switch strings.ToLower(size) {
		case "480p", "720p", "1080p", "4k":
			sizeResolution = strings.ToLower(size)
		case "16:9", "9:16", "portrait", "landscape":
			_, sizeAspect = sizeToApimart(size)
		case "1280x720", "720x1280", "1792x1024", "1024x1792":
			sizeResolution, sizeAspect = sizeToApimart(size)
		case "1920x1080":
			sizeResolution, sizeAspect = "1080p", "16:9"
		case "1080x1920":
			sizeResolution, sizeAspect = "1080p", "9:16"
		default:
			return nil, fmt.Errorf("unsupported video size %q", size)
		}
		if resolution != "" && sizeResolution != "" && !strings.EqualFold(resolution, sizeResolution) {
			return nil, fmt.Errorf("size and resolution conflict")
		}
		if aspect != "" && sizeAspect != "" && aspect != sizeAspect {
			return nil, fmt.Errorf("size and aspect_ratio conflict")
		}
		if resolution == "" {
			resolution = sizeResolution
		}
		if aspect == "" {
			aspect = sizeAspect
		}
	}
	if resolution == "" {
		resolution = "720p"
	}
	resolution = strings.ToLower(resolution)
	if aspect == "" {
		aspect = "16:9"
	}
	model, err := text("model")
	if err != nil {
		return nil, err
	}
	model = normalizeModel(model)
	if isSeedance20(model) || model == ModelSeedance25 {
		valid := resolution == "480p" || resolution == "720p" || resolution == "1080p" || isSeedance20(model) && resolution == "4k"
		if !valid {
			return nil, fmt.Errorf("unsupported %s resolution %q", model, resolution)
		}
		switch aspect {
		case "16:9", "4:3", "1:1", "3:4", "9:16", "21:9", "adaptive":
		default:
			return nil, fmt.Errorf("unsupported aspect_ratio %q", aspect)
		}
	}
	parseDuration := func(v interface{}) (int, error) {
		s := fmt.Sprint(v)
		n, err := strconv.Atoi(s)
		if err != nil {
			return 0, fmt.Errorf("duration and seconds must be integers")
		}
		return n, nil
	}
	if value, ok := fields["seconds"]; ok {
		seconds, err := parseDuration(value)
		if err != nil {
			return nil, err
		}
		if duration, exists := fields["duration"]; exists {
			n, err := parseDuration(duration)
			if err != nil {
				return nil, err
			}
			if n != seconds {
				return nil, fmt.Errorf("duration and seconds conflict")
			}
		} else {
			fields["duration"] = seconds
		}
	}
	if value, ok := fields["duration"]; ok {
		n, err := parseDuration(value)
		if err != nil {
			return nil, err
		}
		if isSeedance20(model) && (n < 4 || n > 30) || model == ModelSeedance25 && n != -1 && (n < 4 || n > 30) {
			return nil, fmt.Errorf("unsupported %s duration", model)
		}
		fields["duration"] = n
	}
	// Keep OpenAI-style image aliases when switching to the shared JSON path.
	if _, exists := fields["image_urls"]; !exists {
		if images, ok := fields["images"]; ok {
			fields["image_urls"] = images
		} else {
			for _, key := range []string{"input_reference", "image"} {
				if value, ok := fields[key].(string); ok && strings.TrimSpace(value) != "" {
					fields["image_urls"] = []string{value}
					break
				}
			}
		}
	}
	fields["resolution"], fields["aspect_ratio"] = resolution, aspect
	delete(fields, "ratio")
	delete(fields, "metadata")
	c.Set("video_requested_spec", requested)
	c.Set("apimart_normalized_request", fields)
	return common.Marshal(fields)
}
