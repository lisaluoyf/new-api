package service

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

const maxSeedanceReferenceBytes = 20 * 1024 * 1024

// NormalizeSeedanceMultipart makes uploaded references part of the reusable
// JSON body before asset routing, validation, billing, and provider selection.
// Other models retain their original multipart transport.
func NormalizeSeedanceMultipart(c *gin.Context) error {
	var fields map[string]any
	if err := common.UnmarshalBodyReusable(c, &fields); err != nil {
		return err
	}
	name, _ := fields["model"].(string)
	if !IsSeedanceLibraryModel(name) {
		return nil
	}
	form, err := common.ParseMultipartFormReusable(c)
	if err != nil {
		return err
	}
	defer form.RemoveAll()
	for key, value := range fields {
		text, ok := value.(string)
		if !ok {
			continue
		}
		switch key {
		case "draft", "return_last_frame", "watermark", "generate_audio", "audio", "nsfw_check", "camerafixed", "web_search":
			v, err := strconv.ParseBool(text)
			if err != nil {
				return fmt.Errorf("%s must be a boolean", key)
			}
			fields[key] = v
		case "duration", "seed":
			v, err := strconv.Atoi(text)
			if err != nil {
				return fmt.Errorf("%s must be an integer", key)
			}
			fields[key] = v
		case "metadata", "image_urls", "images", "image_with_roles", "video_urls", "audio_urls", "video_list", "content", "tools":
			var v any
			if err := common.Unmarshal([]byte(text), &v); err != nil {
				return fmt.Errorf("%s must contain valid JSON", key)
			}
			fields[key] = v
		}
	}
	var uploads []any
	for key := range form.File {
		switch key {
		case "input_reference", "image", "images", "image_urls", "first_frame_image", "last_frame_image":
		default:
			return fmt.Errorf("unsupported reference upload field %s", key)
		}
	}
	// Keep the reference order stable across adapter retries.
	for _, key := range []string{"input_reference", "image", "images", "image_urls", "first_frame_image", "last_frame_image"} {
		files := form.File[key]
		if len(files) == 0 {
			continue
		}
		if (key == "first_frame_image" || key == "last_frame_image") && (len(files) != 1 || fields[key] != nil) {
			return fmt.Errorf("%s requires exactly one reference", key)
		}
		for _, header := range files {
			if header.Size <= 0 || header.Size > maxSeedanceReferenceBytes {
				return fmt.Errorf("reference image must be non-empty and no larger than 20 MB")
			}
			file, err := header.Open()
			if err != nil {
				return fmt.Errorf("cannot read reference image")
			}
			data, readErr := io.ReadAll(io.LimitReader(file, maxSeedanceReferenceBytes+1))
			file.Close()
			if readErr != nil || len(data) == 0 || len(data) > maxSeedanceReferenceBytes {
				return fmt.Errorf("cannot read reference image or image exceeds 20 MB")
			}
			mime := http.DetectContentType(data)
			if mime != "image/png" && mime != "image/jpeg" && mime != "image/webp" {
				return fmt.Errorf("reference image must be PNG, JPEG, or WebP")
			}
			url := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
			if key == "first_frame_image" || key == "last_frame_image" {
				fields[key] = url
			} else {
				uploads = append(uploads, url)
			}
		}
	}
	if len(uploads) > 0 {
		// Explicit URLs and uploaded references both survive; do not hide either.
		refs := []any{}
		for _, key := range []string{"image_urls", "images"} {
			if value, exists := fields[key]; exists {
				switch v := value.(type) {
				case []any:
					refs = append(refs, v...)
				case []string:
					for _, url := range v {
						refs = append(refs, url)
					}
				default:
					return fmt.Errorf("%s must be an array", key)
				}
				delete(fields, key)
			}
		}
		for _, key := range []string{"input_reference", "image"} {
			if value, exists := fields[key]; exists {
				url, ok := value.(string)
				if !ok || strings.TrimSpace(url) == "" {
					return fmt.Errorf("%s must be a non-empty URL", key)
				}
				refs = append(refs, url)
				delete(fields, key)
			}
		}
		fields["image_urls"] = append(refs, uploads...)
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
	c.Set(common.KeyRequestBody, nil)
	c.Request.Body = io.NopCloser(storage)
	c.Request.ContentLength = int64(len(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	return nil
}
