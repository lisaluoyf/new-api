package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	byteplusbase "github.com/byteplus-sdk/byteplus-sdk-golang/base"
	"github.com/gin-gonic/gin"
)

type beeNexAssetCredentials struct {
	bytePlusAssetCredentials
	SigningHost string `json:"signing_host,omitempty"`
}

func isBeeNexSeedance25Channel(ch *model.Channel) bool {
	if ch == nil || ch.Id != constant.BeeNexSeedanceChannelID {
		return false
	}
	u, err := url.Parse(ch.GetBaseURL())
	return err == nil && u.Scheme == "https" && u.Hostname() == "beenexapi.phone580.com" && u.User == nil
}

func loadBeeNexAssetCredentials(channelID int) (beeNexAssetCredentials, error) {
	var c beeNexAssetCredentials
	if channelID != constant.BeeNexSeedanceChannelID {
		return c, seedanceError(503, "BeeNex asset credentials require channel 274", "credentials_not_configured")
	}
	dir := os.Getenv("BEENEX_ASSET_CREDENTIALS_DIR")
	if dir == "" {
		dir = "/run/byteplus-assets"
	}
	f, err := os.Open(filepath.Join(dir, "channel-"+strconv.Itoa(channelID)+".json"))
	if err != nil {
		return c, seedanceError(503, "Separate BeeNex asset signing credentials are not configured", "credentials_not_configured")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 32768 {
		return c, seedanceError(503, "BeeNex asset credential file is invalid", "credentials_not_configured")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 32769))
	if err != nil || common.Unmarshal(raw, &c) != nil || c.AccessKeyID == "" || c.SecretAccessKey == "" || c.Region == "" || c.ProjectName == "" {
		return c, seedanceError(503, "BeeNex asset account, region and project must be explicitly configured", "credentials_not_configured")
	}
	if c.SigningHost != "" && (strings.ContainsAny(c.SigningHost, "/\\?\r\n@ ") || len(c.SigningHost) > 255) {
		return c, seedanceError(503, "BeeNex canonical host configuration is invalid", "credentials_not_configured")
	}
	return c, nil
}

// Test transport hook: production always uses this channel's configured origin.
var beeNexAssetEndpoint = func(ch *model.Channel) string { return strings.TrimRight(ch.GetBaseURL(), "/") + "/post-proxy/" }

func beeNexAssetRequest(ctx context.Context, r *model.SeedanceResource, action string, payload map[string]any) (map[string]any, error) {
	ch, err := model.GetChannelById(r.ChannelID, true)
	if err != nil || !isBeeNexSeedance25Channel(ch) {
		return nil, seedanceError(503, "BeeNex media library channel is unavailable")
	}
	c, err := loadBeeNexAssetCredentials(r.ChannelID)
	if err != nil {
		return nil, err
	}
	if r.ProjectName != "" && (r.ProjectName != c.ProjectName || r.CredentialFingerprint != seedanceControlFingerprint(c.bytePlusAssetCredentials)) {
		return nil, seedanceError(409, "BeeNex asset account or project changed", "asset_context_incompatible")
	}
	fields := make(map[string]any, len(payload)+1)
	for k, v := range payload {
		fields[k] = v
	}
	fields["ProjectName"] = c.ProjectName
	raw, err := common.Marshal(fields)
	if err != nil {
		return nil, err
	}
	target, err := url.Parse(beeNexAssetEndpoint(ch))
	if err != nil {
		return nil, seedanceError(503, "BeeNex asset proxy unavailable")
	}
	target.RawQuery = url.Values{"Action": {action}, "Version": {"2024-01-01"}}.Encode()
	// Sign the canonical URI '/' using the same raw JSON bytes that are sent.
	canonical := *target
	canonical.Path = "/"
	canonical.RawPath = ""
	if c.SigningHost != "" {
		canonical.Host = c.SigningHost
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, canonical.String(), bytes.NewReader(raw))
	if err != nil {
		return nil, seedanceError(502, "BeeNex asset request could not be built")
	}
	req.Header.Set("Content-Type", "application/json")
	creds := byteplusbase.Credentials{AccessKeyID: c.AccessKeyID, SecretAccessKey: c.SecretAccessKey, SessionToken: c.SessionToken, Region: c.Region, Service: "ark"}
	creds.Sign(req)
	req.URL = target
	if c.SigningHost != "" {
		req.Host = c.SigningHost
	}
	client := &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, seedanceError(502, "BeeNex asset proxy could not be reached; query existing resources before retrying")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	var envelope map[string]any
	if err != nil || len(body) > 4<<20 || common.Unmarshal(body, &envelope) != nil || envelope == nil {
		return nil, seedanceError(502, "Invalid BeeNex asset response")
	}
	metadata, _ := envelope["ResponseMetadata"].(map[string]any)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || metadata["Error"] != nil || envelope["error"] != nil {
		return nil, seedanceError(502, fmt.Sprintf("BeeNex asset operation %s could not be confirmed; query existing resources before retrying", action))
	}
	return envelope, nil
}

func BeeNexSeedance25Capabilities() map[string]any {
	return map[string]any{
		"editing":        map[string]any{"supported": false, "reason": "The verified hy model rejects duration=-1; no supported editing protocol is confirmed"},
		"output_formats": []string{"mp4"},
		"mov":            map[string]any{"supported": false, "reason": "Verified BeeNex output is MP4; no platform remux is provided"},
		"last_frame":     map[string]any{"supported": false, "reason": "The verified task query does not return an upstream last-frame resource"},
		"seed":           map[string]any{"accepted": true, "returned": false, "determinism_verified": false},
	}
}

// Match the existing explicitly pinned admin-generation semantics without
// enabling a disabled public ability or changing distribution configuration.
func beeNexMaterialAdminPinned(c *gin.Context, ch *model.Channel, name string) bool {
	if name != "seedance-2.5" || !isBeeNexSeedance25Channel(ch) || ch.Status != common.ChannelStatusEnabled || !model.IsAdmin(c.GetInt("id")) {
		return false
	}
	pinned, ok := common.GetContextKey(c, constant.ContextKeyTokenSpecificChannelId)
	if !ok || pinned != "274" || ValidateChannelClientPolicy(c, ch, name) != nil {
		return false
	}
	for _, m := range ch.GetModels() {
		if m == name {
			return true
		}
	}
	return false
}

func seedanceLibraryChannelAllowed(c *gin.Context, ch *model.Channel, name string) bool {
	return seedanceChannelAllowed(c, ch, name) || beeNexMaterialAdminPinned(c, ch, name)
}
