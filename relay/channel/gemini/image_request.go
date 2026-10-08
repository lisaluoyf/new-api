package gemini

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

func convertGeminiImagineImageRequest(c *gin.Context, info *relaycommon.RelayInfo, req dto.ImageRequest) (*dto.GeminiChatRequest, error) {
	if req.N != nil && *req.N != 1 {
		return nil, fmt.Errorf("Gemini image generation requires n=1")
	}
	if len(req.Mask) > 0 || req.MaskUrl != "" || len(req.Extra["mask_url"]) > 0 {
		return nil, fmt.Errorf("mask is unsupported; use reference images and editing instructions")
	}
	if raw := req.Extra["stream"]; len(raw) > 0 {
		var stream bool
		if common.Unmarshal(raw, &stream) != nil || stream {
			return nil, fmt.Errorf("Gemini Images API does not support stream")
		}
	}
	if req.ResponseFormat != "" && req.ResponseFormat != "url" && req.ResponseFormat != "b64_json" {
		return nil, fmt.Errorf("unsupported response_format")
	}
	if req.ResponseFormat == "b64_json" {
		c.Set("gpt_image2_client_response_format", "b64_json")
	}
	if info.RelayMode == relayconstant.RelayModeImagesEdits || len(req.ImageUrls) > 0 || len(req.Image) > 0 || len(req.Images) > 0 {
		var err error
		req, err = helper.ConvertImageEditsToGeneration(c, req)
		if err != nil {
			return nil, err
		}
	}
	if len(req.ImageUrls) > 14 {
		return nil, fmt.Errorf("Gemini images accept at most 14 image references")
	}
	tier := req.EffectiveResolutionTier()
	if tier != "1K" && tier != "2K" && tier != "4K" || req.Resolution != "" && !strings.EqualFold(strings.TrimSpace(req.Resolution), tier) {
		return nil, fmt.Errorf("unsupported resolution")
	}
	req.Resolution = tier
	aspect, err := geminiImageAspect(req.Size)
	if err != nil {
		return nil, err
	}
	req.Size = aspect
	service.SetImageRequestDataOnContext(c, &req)
	request := buildGeminiImagineRequestFromImage(req)
	for _, ref := range req.ImageUrls {
		var source types.FileSource
		if strings.HasPrefix(ref, "data:image/") {
			mime, data, err := service.DecodeBase64FileData(ref)
			if err != nil {
				return nil, fmt.Errorf("invalid reference image data URI")
			}
			source = types.NewBase64FileSource(data, mime)
		} else if strings.HasPrefix(ref, "https://") || strings.HasPrefix(ref, "http://") {
			source = types.NewURLFileSource(ref)
		} else {
			return nil, fmt.Errorf("reference image must be an HTTP URL or image data URI")
		}
		data, mime, err := service.GetBase64Data(c, source, "Gemini image reference")
		if err != nil {
			return nil, fmt.Errorf("could not read reference image: %w", err)
		}
		if !strings.HasPrefix(mime, "image/") || !geminiSupportedMimeTypes[strings.ToLower(mime)] {
			return nil, fmt.Errorf("unsupported reference image MIME type")
		}
		request.Contents[0].Parts = append(request.Contents[0].Parts, dto.GeminiPart{InlineData: &dto.GeminiInlineData{MimeType: mime, Data: data}})
	}
	return request, nil
}

func geminiImageAspect(size string) (string, error) {
	aspect := strings.ToLower(strings.TrimSpace(size))
	if aspect == "" || aspect == "auto" {
		aspect = "1:1"
	} else if !strings.Contains(aspect, ":") {
		parts := strings.Split(aspect, "x")
		if len(parts) != 2 {
			return "", fmt.Errorf("invalid image size")
		}
		w, ew := strconv.Atoi(parts[0])
		h, eh := strconv.Atoi(parts[1])
		if ew != nil || eh != nil || w <= 0 || h <= 0 {
			return "", fmt.Errorf("invalid image size")
		}
		a, b := w, h
		for b != 0 {
			a, b = b, a%b
		}
		aspect = fmt.Sprintf("%d:%d", w/a, h/a)
	}
	if !map[string]bool{"1:1": true, "1:4": true, "1:8": true, "2:3": true, "3:2": true, "3:4": true, "4:1": true, "4:3": true, "4:5": true, "5:4": true, "8:1": true, "9:16": true, "16:9": true, "21:9": true}[aspect] {
		return "", fmt.Errorf("unsupported image aspect ratio")
	}
	return aspect, nil
}
