package openai

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

var nanoBanana21CacheReference = service.CacheImageBase64Locally

// This provider ignores image_config on Chat Completions. Its native Gemini
// endpoint honors imageSize and accepts inline references at every tier.
func nanoBanana21GeminiBridge(info *relaycommon.RelayInfo) bool {
	if info == nil || info.ChannelMeta == nil || info.ChannelType != constant.ChannelTypeOpenAI || info.OriginModelName != "gemini-nano-banana-2.1" ||
		(info.RelayMode != relayconstant.RelayModeImagesGenerations && info.RelayMode != relayconstant.RelayModeImagesEdits) {
		return false
	}
	base, err := url.Parse(info.ChannelBaseUrl)
	return err == nil && strings.EqualFold(base.Hostname(), "subrouter.ai")
}

// Some OpenAI-compatible image providers return Markdown in message.content
// rather than the OpenRouter message.images field. Only image markup counts;
// ordinary text and links must not be treated as successful image generation.
var nanoBanana21MarkdownImage = regexp.MustCompile(`!\[[^\]\r\n]*\]\(\s*<?(data:image/[A-Za-z0-9.+-]+;base64,[A-Za-z0-9+/=\r\n]+|https?://[^\s<>()]+)>?\s*\)`)

func nanoBanana21ContentImages(raw json.RawMessage) []string {
	var text string
	if common.Unmarshal(raw, &text) != nil {
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if common.Unmarshal(raw, &parts) != nil {
			return nil
		}
		for _, part := range parts {
			if part.Type == "text" {
				text += "\n" + part.Text
			}
		}
	}
	var images []string
	seen := make(map[string]bool)
	for _, match := range nanoBanana21MarkdownImage.FindAllStringSubmatch(text, -1) {
		imageURL := match[1]
		if !seen[imageURL] {
			seen[imageURL] = true
			images = append(images, imageURL)
		}
	}
	return images
}

// The OpenRouter model returns images in chat messages. Keep image requests on
// the existing per-image pricing and settlement path while adapting the wire format.
func nanoBanana21ImageBridge(info *relaycommon.RelayInfo) bool {
	return info != nil && info.OriginModelName == "gemini-nano-banana-2.1" &&
		!nanoBanana21NativeImages(info) &&
		(info.RelayMode == relayconstant.RelayModeImagesGenerations || info.RelayMode == relayconstant.RelayModeImagesEdits)
}

// OpenAI-compatible hubs exposing nano-banana-2.1 implement native Images APIs.
// They require image/images on edits; image_urls on generations is ignored.
func nanoBanana21NativeImages(info *relaycommon.RelayInfo) bool {
	return info != nil && info.ChannelMeta != nil &&
		info.OriginModelName == "gemini-nano-banana-2.1" &&
		info.ChannelType == constant.ChannelTypeOpenAI && info.UpstreamModelName == "nano-banana-2.1" &&
		(info.RelayMode == relayconstant.RelayModeImagesGenerations || info.RelayMode == relayconstant.RelayModeImagesEdits)
}

func convertNanoBanana21ImageRequest(c *gin.Context, info *relaycommon.RelayInfo, req dto.ImageRequest) (any, error) {
	// Reset on each attempt so a retry cannot inherit another channel's path.
	c.Set("nano_banana21_native_edit", false)
	if req.N != nil && *req.N != 1 {
		return nil, fmt.Errorf("Nano Banana 2.1 requires n=1")
	}
	var stream bool
	if raw := req.Extra["stream"]; len(raw) > 0 {
		if err := common.Unmarshal(raw, &stream); err != nil || stream {
			return nil, fmt.Errorf("Nano Banana 2.1 Images API does not support stream")
		}
	}
	if len(req.Mask) > 0 || req.MaskUrl != "" || len(req.Extra["mask_url"]) > 0 {
		return nil, fmt.Errorf("mask is unsupported; provide an image reference and editing instructions")
	}
	if req.ResponseFormat != "" && req.ResponseFormat != "url" && req.ResponseFormat != "b64_json" {
		return nil, fmt.Errorf("unsupported response_format")
	}
	c.Set("nano_banana21_response_format", req.ResponseFormat)
	hasReferences := len(req.ImageUrls) > 0 || (len(req.Image) > 0 && string(req.Image) != "null") || (len(req.Images) > 0 && string(req.Images) != "null")
	if info.RelayMode == relayconstant.RelayModeImagesEdits || hasReferences {
		var err error
		req, err = helper.ConvertImageEditsToGeneration(c, req)
		if err != nil {
			return nil, err
		}
	}
	if len(req.ImageUrls) > 14 {
		return nil, fmt.Errorf("Nano Banana 2.1 accepts at most 14 image references")
	}
	tier := req.EffectiveResolutionTier()
	if tier != "1K" && tier != "2K" && tier != "4K" {
		return nil, fmt.Errorf("unsupported resolution %q", tier)
	}
	if req.Resolution != "" && !strings.EqualFold(strings.TrimSpace(req.Resolution), tier) {
		return nil, fmt.Errorf("unsupported resolution %q", req.Resolution)
	}
	content := []any{map[string]any{"type": "text", "text": req.Prompt}}
	for _, ref := range req.ImageUrls {
		content = append(content, map[string]any{"type": "image_url", "image_url": map[string]string{"url": ref}})
	}
	config := map[string]string{"image_size": tier}
	aspect := "1:1"
	size := strings.ToLower(strings.TrimSpace(req.Size))
	if strings.Contains(size, ":") {
		aspect = size
	} else if size != "" && size != "auto" {
		parts := strings.Split(size, "x")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid image size")
		}
		w, errW := strconv.Atoi(parts[0])
		h, errH := strconv.Atoi(parts[1])
		if errW != nil || errH != nil || w <= 0 || h <= 0 {
			return nil, fmt.Errorf("invalid image size")
		}
		a, b := w, h
		for b != 0 {
			a, b = b, a%b
		}
		aspect = fmt.Sprintf("%d:%d", w/a, h/a)
	}
	supported := map[string]bool{"1:1": true, "1:4": true, "1:8": true, "2:3": true, "3:2": true, "3:4": true, "4:1": true, "4:3": true, "4:5": true, "5:4": true, "8:1": true, "9:16": true, "16:9": true, "21:9": true}
	if !supported[aspect] {
		return nil, fmt.Errorf("unsupported aspect ratio %q; use size as an aspect ratio", aspect)
	}
	config["aspect_ratio"] = aspect
	service.SetImageRequestDataOnContext(c, &req)
	if nanoBanana21GeminiBridge(info) {
		parts := []any{map[string]any{"text": req.Prompt}}
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
			data, mime, err := service.GetBase64Data(c, source, "Nano Banana image reference")
			if err != nil || !strings.HasPrefix(mime, "image/") {
				return nil, fmt.Errorf("could not read reference image")
			}
			parts = append(parts, map[string]any{"inlineData": map[string]string{"mimeType": mime, "data": data}})
		}
		return map[string]any{
			"contents":         []any{map[string]any{"role": "user", "parts": parts}},
			"generationConfig": map[string]any{"responseModalities": []string{"TEXT", "IMAGE"}, "imageConfig": map[string]string{"imageSize": tier, "aspectRatio": aspect}},
		}, nil
	}
	if nanoBanana21NativeImages(info) {
		// These hubs use quality for imageSize; resolution alone is ignored.
		body := map[string]any{"model": req.Model, "prompt": req.Prompt, "n": 1, "size": req.Size, "resolution": tier, "quality": tier, "response_format": "b64_json"}
		if len(req.ImageUrls) > 0 {
			refs := append([]string(nil), req.ImageUrls...)
			for i, ref := range refs {
				if strings.HasPrefix(ref, "data:image/") {
					refs[i] = nanoBanana21CacheReference(ref)
					if refs[i] == "" {
						return nil, fmt.Errorf("could not store reference image for native image editing")
					}
				}
			}
			if len(refs) == 1 {
				body["image"] = refs[0]
			} else {
				body["images"] = refs
			}
			c.Set("nano_banana21_native_edit", true)
		}
		return body, nil
	}
	return map[string]any{"model": req.Model, "messages": []any{map[string]any{"role": "user", "content": content}}, "modalities": []string{"image", "text"}, "image_config": config, "stream": false}, nil
}

func normalizeNanoBanana21ImageResponse(c *gin.Context, info *relaycommon.RelayInfo, raw []byte) ([]byte, error) {
	var response struct {
		Data []struct {
			URL string `json:"url"`
			B64 string `json:"b64_json"`
		} `json:"data"`
		Choices []struct {
			Message struct {
				Content json.RawMessage `json:"content"`
				Images  []struct {
					ImageURL struct {
						URL string `json:"url"`
					} `json:"image_url"`
				} `json:"images"`
			} `json:"message"`
		} `json:"choices"`
		Usage *dto.Usage `json:"usage"`
	}
	if err := common.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	data := []map[string]string{}
	if nanoBanana21GeminiBridge(info) {
		var gemini dto.GeminiChatResponse
		if err := common.Unmarshal(raw, &gemini); err != nil {
			return nil, err
		}
		for _, candidate := range gemini.Candidates {
			for _, part := range candidate.Content.Parts {
				if part.Thought || part.InlineData == nil || !strings.HasPrefix(part.InlineData.MimeType, "image/") || part.InlineData.Data == "" {
					continue
				}
				config, _, _, err := service.DecodeBase64ImageData(part.InlineData.Data)
				if err != nil {
					return nil, fmt.Errorf("invalid upstream generated image")
				}
				if requestData := service.ImageRequestDataFromContext(c); requestData != nil {
					tier, _ := requestData["effective_resolution"].(string)
					minimum := map[string]int{"1K": 1024, "2K": 2048, "4K": 4096}[tier]
					if max(config.Width, config.Height) < minimum {
						return nil, fmt.Errorf("upstream image is smaller than requested resolution")
					}
				}
				data = append(data, map[string]string{"b64_json": part.InlineData.Data})
			}
		}
		response.Usage = &dto.Usage{PromptTokens: gemini.UsageMetadata.PromptTokenCount, CompletionTokens: gemini.UsageMetadata.CandidatesTokenCount, TotalTokens: gemini.UsageMetadata.TotalTokenCount}
	}
	if nanoBanana21NativeImages(info) {
		for _, img := range response.Data {
			if img.B64 != "" {
				data = append(data, map[string]string{"b64_json": img.B64})
			} else if strings.HasPrefix(img.URL, "https://") || strings.HasPrefix(img.URL, "http://") {
				data = append(data, map[string]string{"url": img.URL})
			}
		}
	}
	for _, choice := range response.Choices {
		var imageURLs []string
		for _, img := range choice.Message.Images {
			imageURLs = append(imageURLs, img.ImageURL.URL)
		}
		// Prefer structured images to avoid counting a Markdown representation
		// of the same image twice in settlement.
		if len(imageURLs) == 0 && !nanoBanana21NativeImages(info) && !nanoBanana21GeminiBridge(info) {
			imageURLs = nanoBanana21ContentImages(choice.Message.Content)
		}
		for _, imageURL := range imageURLs {
			u := strings.TrimSpace(imageURL)
			if u == "" {
				continue
			}
			if strings.HasPrefix(u, "data:image/") {
				parts := strings.SplitN(u, ",", 2)
				if len(parts) != 2 || !strings.HasSuffix(parts[0], ";base64") {
					return nil, fmt.Errorf("invalid upstream image data URI")
				}
				decoded, err := base64.StdEncoding.DecodeString(parts[1])
				if err != nil || len(decoded) == 0 {
					return nil, fmt.Errorf("invalid upstream image base64")
				}
				data = append(data, map[string]string{"b64_json": parts[1]})
			} else if strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") {
				data = append(data, map[string]string{"url": u})
			} else {
				return nil, fmt.Errorf("unsupported upstream image URL")
			}
		}
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("upstream returned no generated image")
	}
	info.PriceData.AddOtherRatio("n", float64(len(data)))
	if requestData := service.ImageRequestDataFromContext(c); requestData != nil {
		requestData["actual_image_count"] = len(data)
	}
	if c.GetString("nano_banana21_response_format") == "b64_json" {
		c.Set("gpt_image2_client_response_format", "b64_json")
	}
	return common.Marshal(map[string]any{"created": time.Now().Unix(), "data": data, "usage": response.Usage})
}
