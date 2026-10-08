package openai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

var subrouterGeminiDownload = service.DoDownloadRequest

func isSubrouterGeminiImage(info *relaycommon.RelayInfo, model string) bool {
	if info == nil || info.ChannelMeta == nil {
		return false
	}
	base, err := url.Parse(info.ChannelBaseUrl)
	if err != nil || !strings.EqualFold(base.Hostname(), "subrouter.ai") {
		return false
	}
	switch model {
	case "gemini-3.1-flash-image", "gemini-3.1-flash-image-preview", "gemini-3-pro-image", "gemini-3-pro-image-preview":
		return true
	}
	return false
}

// This upstream's Images API reads aspect ratio from pixel dimensions, not
// from a colon-delimited size or Gemini image_config. Resolution remains a
// separate forwarded field; the upstream may return a higher resolution.
func normalizeSubrouterGeminiImageRequest(info *relaycommon.RelayInfo, request *dto.ImageRequest) {
	if request == nil || !isSubrouterGeminiImage(info, request.Model) {
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

// The upstream ignores image_urls on generations. Download references while
// their signed URLs are valid and upload their bytes to its native edits API.
func convertSubrouterGeminiReferences(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (*bytes.Buffer, error) {
	responseFormat := request.ResponseFormat
	request, err := helper.ConvertImageEditsToGeneration(c, request)
	if err != nil {
		return nil, err
	}
	request.ResponseFormat = responseFormat
	if len(request.ImageUrls) > 14 {
		return nil, fmt.Errorf("at most 14 reference images are supported")
	}
	normalizeSubrouterGeminiImageRequest(info, &request)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	raw, err := common.Marshal(request)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := common.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	for key, value := range fields {
		if key == "image_urls" || key == "image" || key == "images" {
			continue
		}
		var text string
		if common.Unmarshal(value, &text) != nil {
			text = string(value)
		}
		if err := writer.WriteField(key, text); err != nil {
			return nil, err
		}
	}
	for index, reference := range request.ImageUrls {
		parsed, err := url.Parse(reference)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			return nil, fmt.Errorf("reference %d must be an HTTP or HTTPS image URL", index+1)
		}
		response, err := subrouterGeminiDownload(reference, "subrouter_gemini_reference")
		if err != nil {
			return nil, fmt.Errorf("unable to download reference image %d", index+1)
		}
		data, readErr := io.ReadAll(io.LimitReader(response.Body, (20<<20)+1))
		response.Body.Close()
		if response.StatusCode != http.StatusOK || readErr != nil || len(data) == 0 || len(data) > 20<<20 {
			return nil, fmt.Errorf("reference image %d is unavailable or exceeds 20 MB", index+1)
		}
		mimeType := http.DetectContentType(data)
		switch mimeType {
		case "image/png", "image/jpeg", "image/webp", "image/gif":
		default:
			return nil, fmt.Errorf("reference image %d must be PNG, JPEG, WebP or GIF", index+1)
		}
		header := make(textproto.MIMEHeader)
		extension := map[string]string{"image/png": "png", "image/jpeg": "jpg", "image/webp": "webp", "image/gif": "gif"}[mimeType]
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="image"; filename="reference-%d.%s"`, index+1, extension))
		header.Set("Content-Type", mimeType)
		part, err := writer.CreatePart(header)
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(data); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	c.Set("subrouter_gemini_edit_content_type", writer.FormDataContentType())
	c.Set("subrouter_gemini_native_edit", true)
	return &body, nil
}
