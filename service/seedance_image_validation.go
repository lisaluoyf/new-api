package service

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

// Material preparation on VideoFee uses the 300–6000px / 0.4–2.5 contract.
// Validate raw sources before reserving money; asset:// references have already
// been validated by their owning material library and are never fetched as URLs.
func ValidateSeedanceImageInputs(c *gin.Context, fields map[string]any) error {
	urls := seedanceRawImageInputs(fields)
	if len(urls) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	cache, _ := c.Get("seedance_image_dimensions")
	verified, _ := cache.(map[string]image.Config)
	if verified == nil {
		verified = map[string]image.Config{}
		c.Set("seedance_image_dimensions", verified)
	}
	for i, u := range urls {
		if strings.HasPrefix(u, "asset://") {
			continue
		}
		config, ok := verified[u]
		if !ok {
			var err error
			config, err = seedanceImageDimensions(ctx, c, u)
			if err != nil {
				return fmt.Errorf("reference image %d cannot be verified; provide a reachable, supported image smaller than 30 MB", i+1)
			}
			verified[u] = config
		}
		w, h := config.Width, config.Height
		if w < 300 || h < 300 || w > 6000 || h > 6000 {
			return fmt.Errorf("reference image %d dimensions must each be 300-6000 pixels (got %dx%d)", i+1, w, h)
		}
		if w*5 < h*2 || w*2 > h*5 {
			return fmt.Errorf("reference image %d width/height ratio must be 0.4-2.5 (got %dx%d)", i+1, w, h)
		}
	}
	return nil
}

func seedanceRawImageInputs(fields map[string]any) []string {
	var out []string
	seen := map[string]bool{}
	add := func(v any) {
		if s, ok := v.(string); ok && s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, k := range []string{"image", "input_reference", "first_frame_image", "last_frame_image"} {
		add(fields[k])
	}
	for _, k := range []string{"image_urls", "images"} {
		switch items := fields[k].(type) {
		case []any:
			for _, v := range items {
				add(v)
			}
		case []string:
			for _, v := range items {
				add(v)
			}
		}
	}
	if items, ok := fields["image_with_roles"].([]any); ok {
		for _, v := range items {
			if item, ok := v.(map[string]any); ok {
				add(item["url"])
			}
		}
	}
	if items, ok := fields["content"].([]any); ok {
		for _, v := range items {
			if item, ok := v.(map[string]any); ok && item["type"] == "image_url" {
				if source, ok := item["image_url"].(map[string]any); ok {
					add(source["url"])
				}
			}
		}
	}
	if metadata, ok := fields["metadata"].(map[string]any); ok {
		// Metadata content is one supported transport layer, not an arbitrary tree.
		copy := map[string]any{}
		for k, v := range metadata {
			if k != "metadata" {
				copy[k] = v
			}
		}
		for _, u := range seedanceRawImageInputs(copy) {
			add(u)
		}
	}
	return out
}

func seedanceImageDimensions(ctx context.Context, c *gin.Context, u string) (image.Config, error) {
	const limit = 30 * 1024 * 1024
	if strings.HasPrefix(u, "data:") {
		source := types.NewFileSourceFromData(u, "")
		config, _, err := GetImageConfig(c, source)
		if cached := source.GetCache(); cached != nil && cached.Size >= limit {
			return image.Config{}, fmt.Errorf("image too large")
		}
		return config, err
	}
	setting := system_setting.GetFetchSetting()
	if err := common.ValidateURLWithFetchSetting(u, setting.EnableSSRFProtection, setting.AllowPrivateIp, setting.DomainFilterMode, setting.IpFilterMode, setting.DomainList, setting.IpList, setting.AllowedPorts, setting.ApplyIPFilterForDomain); err != nil {
		return image.Config{}, err
	}
	requestURL, method := u, http.MethodGet
	var body io.Reader
	if system_setting.EnableWorker() {
		if !system_setting.WorkerAllowHttpImageRequestEnabled && !strings.HasPrefix(u, "https://") {
			return image.Config{}, fmt.Errorf("HTTPS is required")
		}
		raw, err := common.Marshal(WorkerRequest{URL: u, Key: system_setting.WorkerValidKey})
		if err != nil {
			return image.Config{}, err
		}
		requestURL, method, body = strings.TrimRight(system_setting.WorkerUrl, "/")+"/", http.MethodPost, bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		return image.Config{}, err
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := GetHttpClient().Do(req)
	if err != nil {
		return image.Config{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.ContentLength >= limit {
		return image.Config{}, fmt.Errorf("image unavailable or too large")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil || len(raw) >= limit {
		return image.Config{}, fmt.Errorf("image unavailable or too large")
	}
	config, _, err := decodeImageConfig(raw)
	return config, err
}
