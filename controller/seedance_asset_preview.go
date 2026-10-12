package controller

import (
	"context"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
)

// Fetch the current provider URL only after ownership is confirmed. Signed
// addresses and gateway credentials are never returned or logged here.
func PreviewSeedanceAsset(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("X-Content-Type-Options", "nosniff")
	asset, err := model.GetSeedanceResource(c.GetInt("id"), "asset", c.Param("resource_id"))
	if err != nil {
		c.AbortWithStatus(404)
		return
	}
	if err := service.ValidateSeedanceModelAccess(c, asset.Model); err != nil {
		seedanceLibraryError(c, err)
		return
	}
	if err := service.RefreshSeedanceAsset(c, asset); err != nil {
		seedanceLibraryError(c, err)
		return
	}
	source := service.SeedanceAssetPreviewSource(asset)
	if source == "" {
		c.AbortWithStatus(404)
		return
	}
	settings := system_setting.GetFetchSetting()
	if err := common.ValidateURLWithFetchSetting(source, settings.EnableSSRFProtection, settings.AllowPrivateIp, settings.DomainFilterMode, settings.IpFilterMode, settings.DomainList, settings.IpList, settings.AllowedPorts, settings.ApplyIPFilterForDomain); err != nil {
		c.AbortWithStatus(403)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		c.AbortWithStatus(502)
		return
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		c.AbortWithStatus(502)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		c.AbortWithStatus(502)
		return
	}
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	validType := err == nil && ((asset.AssetType == "Image" && (mediaType == "image/jpeg" || mediaType == "image/png" || mediaType == "image/webp")) || (asset.AssetType == "Video" && strings.HasPrefix(mediaType, "video/")) || (asset.AssetType == "Audio" && strings.HasPrefix(mediaType, "audio/")))
	if !validType {
		c.AbortWithStatus(502)
		return
	}
	c.Header("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	c.Header("Content-Type", resp.Header.Get("Content-Type"))
	c.Status(200)
	_, _ = io.Copy(c.Writer, io.LimitReader(resp.Body, 32<<20))
}
