package openai

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// This upstream's Images API reads aspect ratio from pixel dimensions, not
// from a colon-delimited size or Gemini image_config. Resolution remains a
// separate forwarded field; the upstream may return a higher resolution.
func normalizeSubrouterGeminiImageRequest(info *relaycommon.RelayInfo, request *dto.ImageRequest) {
	if info == nil || info.ChannelMeta == nil || request == nil {
		return
	}
	base, err := url.Parse(info.ChannelBaseUrl)
	if err != nil || !strings.EqualFold(base.Hostname(), "subrouter.ai") {
		return
	}
	switch request.Model {
	case "gemini-3.1-flash-image", "gemini-3.1-flash-image-preview", "gemini-3-pro-image", "gemini-3-pro-image-preview":
	default:
		return
	}
	dimensions := map[string][2]int{
		"1:1": {1024, 1024}, "2:3": {1024, 1536}, "3:2": {1536, 1024},
		"3:4": {768, 1024}, "4:3": {1024, 768}, "4:5": {1024, 1280},
		"5:4": {1280, 1024}, "9:16": {576, 1024}, "16:9": {1024, 576},
		"21:9": {1792, 768},
	}
	size, ok := dimensions[strings.TrimSpace(request.Size)]
	if !ok {
		return
	}
	scale := 1
	switch strings.ToUpper(strings.TrimSpace(request.Resolution)) {
	case "2K":
		scale = 2
	case "4K":
		scale = 4
	}
	request.Size = fmt.Sprintf("%dx%d", size[0]*scale, size[1]*scale)
}
