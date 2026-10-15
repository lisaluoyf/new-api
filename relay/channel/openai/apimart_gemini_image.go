package openai

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/gin-gonic/gin"
)

const apimartGeminiResponseFormatKey = "apimart_gemini_client_response_format"

// APIMart combines Gemini generation and reference editing on generations;
// its edits endpoint is Grok-only. Match exact provider hosts, not lookalikes.
func IsAPIMartGeminiImage(info *relaycommon.RelayInfo) bool {
	if info == nil || info.ChannelMeta == nil || info.ChannelType != constant.ChannelTypeOpenAI ||
		(info.RelayMode != relayconstant.RelayModeImagesGenerations && info.RelayMode != relayconstant.RelayModeImagesEdits) {
		return false
	}
	if info.OriginModelName != "gemini-3-pro-image" && info.OriginModelName != "gemini-3-pro-image-preview" {
		return false
	}
	base, err := url.Parse(info.ChannelBaseUrl)
	return err == nil && (strings.EqualFold(base.Hostname(), "api.apib.ai") || strings.EqualFold(base.Hostname(), "api.apimart.ai"))
}

func convertAPIMartGeminiImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (dto.ImageRequest, error) {
	format := request.ResponseFormat
	if format != "" && format != "url" && format != "b64_json" {
		return request, fmt.Errorf("unsupported response_format")
	}
	hasReferences := len(request.ImageUrls) > 0 || (len(request.Image) > 0 && string(request.Image) != "null") || (len(request.Images) > 0 && string(request.Images) != "null")
	if info.RelayMode == relayconstant.RelayModeImagesEdits || hasReferences {
		var err error
		request, err = helper.ConvertImageEditsToGeneration(c, request)
		if err != nil {
			return request, err
		}
	}
	if len(request.ImageUrls) > 14 {
		return request, fmt.Errorf("Gemini Pro Image accepts at most 14 reference images")
	}
	if request.N != nil && *request.N != 1 {
		return request, fmt.Errorf("this Gemini Pro Image provider requires n=1")
	}
	if (len(request.Mask) > 0 && string(request.Mask) != "null") || request.MaskUrl != "" || len(request.Extra["mask_url"]) > 0 {
		return request, fmt.Errorf("mask is unsupported; provide an image reference and editing instructions")
	}
	// Preserve the client's requested format locally. This provider accepts url
	// only; the shared response handler converts completed images to b64_json.
	c.Set(apimartGeminiResponseFormatKey, format)
	request.ResponseFormat = "url"
	return request, nil
}
