package beenex

import "fmt"

// Editing and extension use BeeNex's verified Ark endpoint. Ordinary generation
// retains the existing compatible transport. Task type is inferred upstream
// from the caller's prompt; the provider rejects the explicit type parameter.
func nativeGeneration(name string, fields map[string]any) bool {
	if name != "seedance-2.5" {
		return false
	}
	kind, _ := fields["omni_reference_task_type"].(string)
	if kind == "edit" || kind == "extend" {
		return true
	}
	return fields["duration"] == float64(-1) && lenVideoReferences(fields) > 0
}

func lenVideoReferences(fields map[string]any) int {
	if values, ok := fields["video_urls"].([]any); ok {
		return len(values)
	}
	if values, ok := fields["content"].([]any); ok {
		n := 0
		for _, raw := range values {
			if item, ok := raw.(map[string]any); ok && item["type"] == "video_url" {
				n++
			}
		}
		return n
	}
	return 0
}

func generationPayload(name string, fields map[string]any) (map[string]any, error) {
	compatible, err := contentPayload(fields)
	if err != nil || !nativeGeneration(name, fields) {
		return compatible, err
	}
	if lenVideoReferences(fields) == 0 {
		return nil, fmt.Errorf("editing and extension require a reference video")
	}
	if fields["aspect_ratio"] != "adaptive" {
		return nil, fmt.Errorf("editing and extension require adaptive ratio")
	}
	if fields["omni_reference_task_type"] == "edit" && fields["duration"] != float64(-1) {
		return nil, fmt.Errorf("edit duration must be -1")
	}
	metadata := compatible["metadata"].(map[string]any)
	out := map[string]any{}
	for _, key := range []string{"duration", "resolution", "generate_audio", "watermark", "seed"} {
		if value, exists := metadata[key]; exists {
			out[key] = value
		}
	}
	out["ratio"] = "adaptive"
	content := []any{map[string]any{"type": "text", "text": fields["prompt"]}}
	if media, ok := metadata["content"].([]any); ok {
		content = append(content, media...)
	}
	out["content"] = content
	return out, nil
}
