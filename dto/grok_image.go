package dto

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

const GrokImage20Model = "grok-imagine-image-2.0"

func IsGrokImage20(model string) bool {
	return strings.EqualFold(strings.TrimSpace(model), GrokImage20Model)
}

// Pin auto quality before pricing and forwarding: xAI defaults to low for
// generation and medium for reference editing. Explicit values remain explicit.
func (r *ImageRequest) NormalizeGrokImage20() error {
	if !IsGrokImage20(r.Model) {
		return nil
	}
	if strings.TrimSpace(r.Prompt) == "" {
		return fmt.Errorf("prompt is required")
	}
	if len(r.Image) > 0 || len(r.Images) > 0 || len(r.Mask) > 0 || r.MaskUrl != "" {
		return fmt.Errorf("grok-imagine-image-2.0 references must use image_urls")
	}
	if len(r.ImageUrls) > 5 {
		return fmt.Errorf("grok-imagine-image-2.0 supports at most 5 reference images")
	}
	seenReferences := make(map[string]bool, len(r.ImageUrls))
	for _, url := range r.ImageUrls {
		if strings.TrimSpace(url) == "" {
			return fmt.Errorf("reference image URL cannot be empty")
		}
		if seenReferences[strings.TrimSpace(url)] {
			return fmt.Errorf("image_urls must not contain duplicate URLs")
		}
		seenReferences[strings.TrimSpace(url)] = true
	}
	if r.N == nil {
		r.N = common.GetPointer(uint(1))
	}
	if *r.N < 1 || *r.N > 10 {
		return fmt.Errorf("grok-imagine-image-2.0 n must be between 1 and 10")
	}
	r.Resolution = strings.ToLower(strings.TrimSpace(r.Resolution))
	if r.Resolution == "" {
		r.Resolution = "1k"
	}
	if r.Resolution != "1k" && r.Resolution != "2k" {
		return fmt.Errorf("grok-imagine-image-2.0 resolution must be 1k or 2k")
	}
	r.Quality = strings.ToLower(strings.TrimSpace(r.Quality))
	if r.Quality == "" || r.Quality == "auto" {
		r.Quality = "low"
		if len(r.ImageUrls) > 0 {
			r.Quality = "medium"
		}
	}
	if r.Quality != "low" && r.Quality != "medium" {
		return fmt.Errorf("grok-imagine-image-2.0 quality must be low, medium or auto")
	}
	if r.Extra == nil {
		r.Extra = make(map[string]json.RawMessage)
	}
	if r.Size != "" {
		if !strings.Contains(r.Size, ":") {
			return fmt.Errorf("use resolution=1k/2k and aspect_ratio for grok-imagine-image-2.0")
		}
		var aspect string
		if raw := r.Extra["aspect_ratio"]; len(raw) > 0 {
			if common.Unmarshal(raw, &aspect) != nil || aspect != r.Size {
				return fmt.Errorf("size conflicts with aspect_ratio")
			}
		}
		r.Extra["aspect_ratio"], _ = common.Marshal(r.Size)
		r.Size = ""
	}
	return nil
}

func (r *ImageRequest) GrokImagePriceVariant() string {
	resolution := strings.ToUpper(strings.TrimSpace(r.Resolution))
	if resolution == "" {
		resolution = "1K"
	}
	quality := strings.ToLower(strings.TrimSpace(r.Quality))
	if quality == "" || quality == "auto" {
		quality = "low"
		if len(r.ImageUrls) > 0 {
			quality = "medium"
		}
	}
	return resolution + " " + quality
}
