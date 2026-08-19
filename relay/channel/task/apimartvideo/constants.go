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
	ModelKlingV3MotionControl,
}

var ChannelName = "apimart-video"

func IsVideoModel(model string) bool {
	switch strings.TrimSpace(model) {
	case "sora", "sora-2", "sora-2-pro", ModelDoubaoSeedance20,
		ModelGrokImagineVideo15, ModelGrokVideo10s, ModelGrokVideo15s, ModelGrokVideo6s,
		ModelKlingV3MotionControl:
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

func modeBillingRatio(mode string) float64 {
	if strings.EqualFold(strings.TrimSpace(mode), "pro") {
		return ProUSDPerSecond / StdUSDPerSecond
	}
	return 1
}

func defaultBillableSeconds(orientation string) int {
	if strings.EqualFold(strings.TrimSpace(orientation), "video") {
		return 30
	}
	return 10
}
