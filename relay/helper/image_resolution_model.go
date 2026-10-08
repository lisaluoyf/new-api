package helper

import (
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"strings"
)

func HasImageResolutionModelMapping(info *relaycommon.RelayInfo) bool {
	return info != nil && info.ChannelMeta != nil && len(info.ChannelSetting.ImageResolutionModelMapping[info.OriginModelName]) > 0
}

// Apply after static mapping and before serialization, including multipart edits.
// The original model and requested resolution remain unchanged for billing.
func ImageResolutionModelMappedHelper(info *relaycommon.RelayInfo, request *dto.ImageRequest) {
	if request == nil || !HasImageResolutionModelMapping(info) {
		return
	}
	resolution := strings.ToLower(strings.TrimSpace(request.Resolution))
	if resolution == "" {
		resolution = "1k"
	}
	upstream := info.ChannelSetting.ImageResolutionModelMapping[info.OriginModelName][resolution]
	if upstream == "" {
		return
	}
	request.Model = upstream
	info.UpstreamModelName = upstream
	info.IsModelMapped = upstream != info.OriginModelName
}
