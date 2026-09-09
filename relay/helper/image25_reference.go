package helper

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// Normalize before channel selection so capability checks, retries and billing
// all see the same JSON request that the Image 2.5 upstream accepts.
func NormalizeGptImage25ReferenceRequest(c *gin.Context, modelName string) error {
	if !service.IsGptImage25Model(modelName) || c == nil || c.Request == nil {
		return nil
	}
	path := c.Request.URL.Path
	isEdit := path == "/v1/images/edits"
	if !isEdit && path != "/v1/images/generations" && path != "/v1/images/generations/async" {
		return nil
	}
	isMultipart := strings.HasPrefix(c.ContentType(), "multipart/form-data")
	if !isEdit && !isMultipart {
		return nil
	}
	fields := make(map[string]json.RawMessage)
	var uploaded []string
	if isMultipart {
		form, err := common.ParseMultipartFormReusable(c)
		if err != nil {
			return fmt.Errorf("invalid image form: %w", err)
		}
		for name := range form.File {
			if name != "images" && name != "image" && !strings.HasPrefix(name, "image[") {
				return fmt.Errorf("unsupported Image 2.5 file field %q; use image or image[]", name)
			}
		}
		for name, values := range form.Value {
			if len(values) == 0 {
				continue
			}
			value := values[0]
			switch name {
			case "n", "output_compression", "partial_images":
				if _, err := strconv.ParseUint(value, 10, 32); err != nil {
					return fmt.Errorf("invalid image field %s", name)
				}
				fields[name] = json.RawMessage(value)
			case "stream", "watermark":
				if value != "true" && value != "false" {
					return fmt.Errorf("invalid image field %s", name)
				}
				fields[name] = json.RawMessage(value)
			case "image_urls":
				fields[name] = json.RawMessage(value)
			default:
				fields[name], _ = common.Marshal(value)
			}
		}
		uploaded, err = imageDataURIsFromMultipart(form)
		if err != nil {
			return err
		}
	} else if err := common.UnmarshalBodyReusable(c, &fields); err != nil {
		return fmt.Errorf("invalid image JSON: %w", err)
	}
	if fields == nil {
		return fmt.Errorf("image request must be an object")
	}
	if _, exists := fields["mask"]; exists {
		return fmt.Errorf("Image 2.5 uploaded masks are unsupported; use mask_url")
	}
	var urls []string
	if raw, exists := fields["image_urls"]; exists {
		if err := common.Unmarshal(raw, &urls); err != nil {
			return fmt.Errorf("image_urls must be an array of strings")
		}
	}
	if raw, exists := fields["image"]; exists {
		var url string
		if err := common.Unmarshal(raw, &url); err != nil || strings.TrimSpace(url) == "" {
			return fmt.Errorf("image must be a reference URL or data URI")
		}
		urls = append(urls, url)
		delete(fields, "image")
	}
	urls = append(urls, uploaded...)
	if isEdit && len(urls) == 0 {
		return fmt.Errorf("image edit requires at least one reference image")
	}
	if len(urls) > 0 {
		fields["image_urls"], _ = common.Marshal(urls)
	}
	raw, err := common.Marshal(fields)
	if err != nil {
		return err
	}
	storage, err := common.CreateBodyStorage(raw)
	if err != nil {
		return err
	}
	common.CleanupBodyStorage(c)
	c.Set(common.KeyBodyStorage, storage)
	c.Set(common.KeyRequestBody, raw)
	c.Request.Body = io.NopCloser(storage)
	c.Request.ContentLength = int64(len(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	if isEdit {
		c.Request.URL.Path = "/v1/images/generations"
	}
	c.Set("relay_mode", relayconstant.RelayModeImagesGenerations)
	return nil
}
