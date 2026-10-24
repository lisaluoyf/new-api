package beenex

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
)

// Editing and extension use BeeNex's verified Ark endpoint. Ordinary generation
// retains the existing compatible transport. Task type is inferred upstream
// from the caller's prompt; the provider rejects the explicit type parameter.
func nativeGeneration(name string, fields map[string]any) bool {
	if name != "seedance-2.5" && name != "seedance-2.0" {
		return false
	}
	metadata, _ := fields["metadata"].(map[string]any)
	if raw, ok := fields["metadata"].(string); ok {
		_ = common.Unmarshal([]byte(raw), &metadata)
	}
	if len(metadata) > 0 {
		merged := make(map[string]any, len(fields)+len(metadata))
		for key, value := range metadata {
			merged[key] = value
		}
		for key, value := range fields {
			merged[key] = value
		}
		fields = merged
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
	if name == "seedance-2.0" {
		duration, ok := fields["duration"].(float64)
		if !ok || (duration != -1 && (duration < 4 || duration > 15)) {
			return nil, fmt.Errorf("BeeNex Seedance 2.0 editing and extension require duration -1 or 4 to 15 seconds")
		}
	}
	if lenVideoReferences(fields) == 0 {
		return nil, fmt.Errorf("editing and extension require a reference video")
	}
	if name == "seedance-2.5" && fields["aspect_ratio"] != "adaptive" {
		return nil, fmt.Errorf("editing and extension require adaptive ratio")
	}
	if name == "seedance-2.5" && fields["omni_reference_task_type"] == "edit" && fields["duration"] != float64(-1) {
		return nil, fmt.Errorf("edit duration must be -1")
	}
	if _, present := fields["seed"]; present {
		return nil, fmt.Errorf("BeeNex native editing and extension do not support seed; omit seed")
	}
	metadata := compatible["metadata"].(map[string]any)
	out := map[string]any{}
	for _, key := range []string{"duration", "resolution", "generate_audio", "watermark"} {
		if value, exists := metadata[key]; exists {
			out[key] = value
		}
	}
	out["ratio"] = fields["aspect_ratio"]
	content := []any{map[string]any{"type": "text", "text": fields["prompt"]}}
	if media, ok := metadata["content"].([]any); ok {
		content = append(content, media...)
	}
	out["content"] = content
	return out, nil
}
