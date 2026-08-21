package apimartvideo

import (
	"strings"
)

const (
	ModelKlingV3MotionControl = "kling-v3-motion-control"
	ModelDoubaoSeedance20     = "doubao-seedance-2.0"
	ModelGrokImagineVideo15   = "grok-imagine-video-1.5"
	ModelGrokVideo10s         = "grok-1.5-video-10s"
	ModelGrokVideo15s         = "grok-1.5-video-15s"
	ModelGrokVideo6s          = "grok-1.5-video-6s"
	ModelKlingV3Omni          = "kling-v3-omni"
	// StdUSDPerSecond is APIMart purchase price for mode=std.
	StdUSDPerSecond = 0.10288
	// ProUSDPerSecond is APIMart purchase price for mode=pro.
	ProUSDPerSecond = 0.13712
)

var ModelList = []string{
	"sora",
	"sora-2",
	"sora-2-pro",
	ModelDoubaoSeedance20,
	ModelGrokImagineVideo15,
	ModelGrokVideo10s,
	ModelGrokVideo15s,
	ModelGrokVideo6s,
	ModelKlingV3Omni,
	ModelKlingV3MotionControl,
}

var ChannelName = "apimart-video"

func IsVideoModel(model string) bool {
	switch strings.TrimSpace(model) {
	case "sora", "sora-2", "sora-2-pro", ModelDoubaoSeedance20,
		ModelGrokImagineVideo15, ModelGrokVideo10s, ModelGrokVideo15s, ModelGrokVideo6s,
		ModelKlingV3Omni, ModelKlingV3MotionControl:
		return true
	default:
		return false
	}
}

func IsMotionControlModel(model string) bool {
	return strings.TrimSpace(model) == ModelKlingV3MotionControl
}

func IsChannel(baseURL string) bool {
	baseURL = strings.ToLower(strings.TrimSpace(baseURL))
	return strings.Contains(baseURL, "apimart.ai") || strings.Contains(baseURL, "apib.ai")
}

func normalizeModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "sora" {
		return "sora-2"
	}
	return model
}

func normalizeVideoDuration(model string, seconds int) int {
	if normalizeModel(model) == ModelKlingV3Omni {
		if seconds >= 3 && seconds <= 15 {
			return seconds
		}
		return 5
	}
	if seconds <= 0 {
		seconds = 4
	}
	minimum := 0
	switch normalizeModel(model) {
	case ModelGrokImagineVideo15, ModelGrokVideo6s:
		minimum = 6
	case ModelGrokVideo10s:
		minimum = 10
	case ModelGrokVideo15s:
		minimum = 15
	}
	if seconds < minimum {
		return minimum
	}
	return seconds
}

func modeBillingRatio(mode string) float64 {
	if strings.EqualFold(strings.TrimSpace(mode), "pro") {
		return ProUSDPerSecond / StdUSDPerSecond
	}
	return 1
}

func klingOmniBillingVariant(mode string, audio, hasVideo bool) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "4k":
		if audio && !hasVideo {
			return "4k-sound"
		}
		// APIMart currently publishes no separate 4K reference-video tier.
		return "4k"
	case "pro":
		if hasVideo {
			return "pro-video"
		}
		if audio {
			return "pro-sound"
		}
		return "pro"
	default:
		if hasVideo {
			return "video"
		}
		if audio {
			return "sound"
		}
		return "base"
	}
}

func defaultBillableSeconds(orientation string) int {
	if strings.EqualFold(strings.TrimSpace(orientation), "video") {
		return 30
	}
	return 10
}
