package apimartvideo

import (
	"fmt"
	"strings"
)

func validateSeedanceVariantFields(fields map[string]any) error {
	modelName, _ := fields["model"].(string)
	if !isSeedance20Variant(modelName) {
		return nil
	}
	prompt, ok := fields["prompt"].(string)
	if !ok || strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("prompt is required")
	}
	counts := map[string]int{}
	for key, limit := range map[string]int{"image_urls": 9, "video_urls": 3, "audio_urls": 3} {
		if v, exists := fields[key]; exists {
			urls, ok := v.([]any)
			if !ok {
				return fmt.Errorf("%s must be an array of strings", key)
			}
			if len(urls) > limit {
				return fmt.Errorf("%s supports at most %d inputs", key, limit)
			}
			for _, u := range urls {
				if s, ok := u.(string); !ok || strings.TrimSpace(s) == "" {
					return fmt.Errorf("%s entries must be non-empty strings", key)
				}
			}
			counts[key] = len(urls)
		}
	}
	if value, exists := fields["image_with_roles"]; exists {
		if _, exists := fields["image_urls"]; exists {
			return fmt.Errorf("image_urls and image_with_roles cannot be used together")
		}
		entries, ok := value.([]any)
		if !ok || len(entries) > 9 {
			return fmt.Errorf("image_with_roles must be an array with at most 9 images")
		}
		roles := map[string]int{}
		for _, v := range entries {
			entry, ok := v.(map[string]any)
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
		}
		counts["image_urls"] = len(entries)
		if roles["first_frame"] > 1 || roles["last_frame"] > 1 {
			return fmt.Errorf("Use at most one first_frame and one last_frame")
		}
		if roles["last_frame"] > 0 && roles["first_frame"] == 0 {
			return fmt.Errorf("last_frame requires first_frame")
		}
		if roles["first_frame"] > 0 {
			if counts["video_urls"] > 0 || counts["audio_urls"] > 0 {
				return fmt.Errorf("First/last-frame images cannot be combined with video_urls or audio_urls")
			}
			if fields["aspect_ratio"] != "adaptive" {
				return fmt.Errorf("First/last-frame tasks require adaptive aspect_ratio")
			}
		}
	}
	if counts["audio_urls"] > 0 && counts["image_urls"] == 0 && counts["video_urls"] == 0 {
		return fmt.Errorf("audio_urls requires image or video references")
	}
	if audio, exists := fields["audio"]; exists {
		value, ok := audio.(bool)
		if !ok {
			return fmt.Errorf("audio must be a boolean")
		}
		if generation, exists := fields["generate_audio"]; exists && generation != value {
			return fmt.Errorf("audio and generate_audio conflict")
		}
		fields["generate_audio"] = value
		delete(fields, "audio")
	}
	return nil
}
