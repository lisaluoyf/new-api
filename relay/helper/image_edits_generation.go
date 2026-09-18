package helper

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
)

// ConvertImageEditsToGeneration builds an attempt-local JSON request. It does
// not replace the stored body, URL or multipart form, which native retries need.
func ConvertImageEditsToGeneration(c *gin.Context, request dto.ImageRequest) (dto.ImageRequest, error) {
	var uploaded []string
	if c.Request.MultipartForm != nil || strings.Contains(c.GetHeader("Content-Type"), "multipart/form-data") {
		form, err := common.ParseMultipartFormReusable(c)
		if err != nil {
			return request, err
		}
		defer form.RemoveAll()
		for name := range form.File {
			if name != "image" && name != "images" && !strings.HasPrefix(name, "image[") {
				return request, fmt.Errorf("unsupported image edit file field %q", name)
			}
		}
		fields := make(map[string]json.RawMessage)
		for name, values := range form.Value {
			if len(values) == 0 {
				continue
			}
			value := values[0]
			switch name {
			case "n", "output_compression", "partial_images":
				if _, err := strconv.ParseUint(value, 10, 32); err != nil {
					return request, fmt.Errorf("invalid image field %s", name)
				}
				fields[name] = json.RawMessage(value)
			case "stream", "watermark":
				if value != "true" && value != "false" {
					return request, fmt.Errorf("invalid image field %s", name)
				}
				fields[name] = json.RawMessage(value)
			case "image_urls", "images":
				fields[name] = json.RawMessage(value)
			default:
				fields[name], _ = common.Marshal(value)
			}
		}
		// Model mapping has already run; the multipart client model must not undo it.
		fields["model"], _ = common.Marshal(request.Model)
		raw, err := common.Marshal(fields)
		if err != nil {
			return request, err
		}
		if err := common.Unmarshal(raw, &request); err != nil {
			return request, err
		}
		uploaded, err = imageDataURIsFromMultipart(form)
		if err != nil {
			return request, err
		}
	}
	if len(request.Mask) > 0 && string(request.Mask) != "null" {
		return request, fmt.Errorf("mask is unsupported by image edit conversion; use mask_url on a compatible channel")
	}
	urls := append([]string(nil), request.ImageUrls...)
	for _, raw := range []json.RawMessage{request.Image, request.Images} {
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var single string
		if common.Unmarshal(raw, &single) == nil {
			urls = append(urls, single)
			continue
		}
		var multiple []string
		if common.Unmarshal(raw, &multiple) != nil {
			return request, fmt.Errorf("image references must be URLs or data URIs")
		}
		urls = append(urls, multiple...)
	}
	urls = append(urls, uploaded...)
	if len(urls) == 0 {
		return request, fmt.Errorf("image edit requires at least one reference image")
	}
	for _, ref := range urls {
		if strings.TrimSpace(ref) == "" {
			return request, fmt.Errorf("image reference cannot be empty")
		}
	}
	request.ImageUrls, request.Image, request.Images = urls, nil, nil
	request.ResponseFormat = "" // converted locally when returning the image
	if request.N == nil || *request.N == 0 {
		request.N = common.GetPointer(uint(1))
	}
	return request, nil
}
