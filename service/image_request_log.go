package service

import (
	"strings"

	"github.com/QuantumNous/new-api/dto"
)

const imageRequestDataContextKey = "image_request_data"

// SetImageRequestDataOnContext stores sanitized request params for usage log preview.
func SetImageRequestDataOnContext(c interface{ Set(string, any) }, req *dto.ImageRequest) {
	if c == nil || req == nil {
		return
	}
	if data := BuildImageRequestDataForLog(req); len(data) > 0 {
		c.Set(imageRequestDataContextKey, data)
	}
}

// ImageRequestDataFromContext reads request params stashed during image relay.
func ImageRequestDataFromContext(c interface{ Get(string) (any, bool) }) map[string]interface{} {
	if c == nil {
		return nil
	}
	raw, ok := c.Get(imageRequestDataContextKey)
	if !ok || raw == nil {
		return nil
	}
	if m, ok := raw.(map[string]interface{}); ok && len(m) > 0 {
		return m
	}
	return nil
}

// BuildImageRequestDataForLog returns user-facing request fields for log preview.
func BuildImageRequestDataForLog(req *dto.ImageRequest) map[string]interface{} {
	if req == nil {
		return nil
	}

	imageN := uint(1)
	if req.N != nil && *req.N > 0 {
		imageN = *req.N
	}

	data := map[string]interface{}{
		"model":              strings.TrimSpace(req.Model),
		"prompt":             req.Prompt,
		"n":                  imageN,
		"actual_image_count": imageN,
	}
	if size := strings.TrimSpace(req.Size); size != "" {
		data["size"] = size
	}
	if resolution := strings.TrimSpace(req.Resolution); resolution != "" {
		data["resolution"] = strings.ToLower(resolution)
	}
	data["effective_resolution"] = req.EffectiveResolutionTier()
	if ratio := dto.GeminiFlashImageResolutionPriceRatio(req.Resolution); strings.Contains(strings.ToLower(strings.TrimSpace(req.Model)), "flash-image") && ratio != 1.0 {
		data["resolution_price_ratio"] = ratio
	}
	if quality := strings.TrimSpace(req.Quality); quality != "" {
		data["quality"] = quality
	}
	if urls := imageURLsForLog(req.ImageUrls); len(urls) > 0 {
		data["image_urls"] = urls
	}
	return data
}

func imageURLsForLog(urls []string) []string {
	if len(urls) == 0 {
		return nil
	}
	filtered := make([]string, 0, len(urls))
	for _, raw := range urls {
		u := strings.TrimSpace(raw)
		if u == "" || strings.HasPrefix(strings.ToLower(u), "data:image") {
			continue
		}
		filtered = append(filtered, u)
	}
	return filtered
}
