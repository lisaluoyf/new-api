package service

import (
	"bytes"
	"context"
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
	SigningHost                       string           `json:"signing_host,omitempty"`
	ProjectMode                       string           `json:"project_mode,omitempty"`
	AccountScopedProjects             []string         `json:"account_scoped_projects,omitempty"`
	OfficialH5Enabled                 bool             `json:"official_h5_enabled,omitempty"`
	MaterialVerifiedAt                int64            `json:"material_verified_at,omitempty"`
	MaterialVerifiedFingerprint       string           `json:"material_verified_fingerprint,omitempty"`
	MaterialVerifiedModels            map[string]int64 `json:"material_verified_models,omitempty"`
	GenerationReferenceVerifiedModels map[string]int64 `json:"generation_reference_verified_models,omitempty"`
}

func IsBeeNexSeedanceModel(name string) bool {
	return name == "seedance-2.0" || name == "seedance-2.5"
}

// Preserve the previously verified 2.5 marker without inferring acceptance for 2.0.
func beeNexMaterialVerifiedAt(c beeNexAssetCredentials, name string) int64 {
	if c.MaterialVerifiedFingerprint != beeNexControlFingerprint(c) {
		return 0
	}
	if at := c.MaterialVerifiedModels[name]; at > 0 {
		return at
	}
	if name == "seedance-2.5" {
		return c.MaterialVerifiedAt
	}
	return 0
}

func isBeeNexSeedanceChannel(ch *model.Channel) bool {
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
	if err != nil || common.Unmarshal(raw, &c) != nil || c.AccessKeyID == "" || c.SecretAccessKey == "" || c.Region == "" {
		return c, seedanceError(503, "BeeNex asset account, region and project must be explicitly configured", "credentials_not_configured")
	}
	if c.ProjectMode == "" {
		c.ProjectMode = "named"
	}
	if (c.ProjectMode == "named" && c.ProjectName == "") || (c.ProjectMode == "account_scoped" && c.ProjectName != "") || (c.ProjectMode != "named" && c.ProjectMode != "account_scoped") {
		return c, seedanceError(503, "BeeNex asset project scope must be explicitly configured", "credentials_not_configured")
	}
	if c.Region != "cn-beijing" {
		return c, seedanceError(503, "BeeNex asset signing requires the documented cn-beijing region", "credentials_not_configured")
	}
	if c.SigningHost != "" && (strings.ContainsAny(c.SigningHost, "/\\?\r\n@ ") || len(c.SigningHost) > 255) {
		return c, seedanceError(503, "BeeNex canonical host configuration is invalid", "credentials_not_configured")
	}
	return c, nil
}

func beeNexControlFingerprint(c beeNexAssetCredentials) string {
	return SeedanceKeyFingerprint(c.AccessKeyID + "\x00" + c.ProjectMode + "\x00" + c.ProjectName)
}

func validateBeeNexAssetContext(r *model.SeedanceResource, c beeNexAssetCredentials) error {
	if r.ChannelID != constant.BeeNexSeedanceChannelID || r.CredentialFingerprint == "" || r.ProjectName != c.ProjectName || r.CredentialFingerprint != beeNexControlFingerprint(c) {
		return seedanceError(409, "BeeNex asset account or project changed", "asset_context_incompatible")
	}
	return nil
}

// Test transport hook: production always uses this channel's configured origin.
var beeNexAssetEndpoint = func(ch *model.Channel) string { return strings.TrimRight(ch.GetBaseURL(), "/") + "/post-proxy/" }

func beeNexAssetRequest(ctx context.Context, r *model.SeedanceResource, action string, payload map[string]any) (map[string]any, error) {
	return beeNexRetryNewAssetRead(ctx, r, action, payload, func() (map[string]any, error) {
		return beeNexAssetRequestOnce(ctx, r, action, payload)
	})
}

func beeNexAssetRequestOnce(ctx context.Context, r *model.SeedanceResource, action string, payload map[string]any) (map[string]any, error) {
	ch, err := model.GetChannelById(r.ChannelID, true)
	if err != nil || !isBeeNexSeedanceChannel(ch) {
		return nil, seedanceError(503, "BeeNex media library channel is unavailable")
	}
	c, err := loadBeeNexAssetCredentials(r.ChannelID)
	if err != nil {
		return nil, err
	}
	if err := validateBeeNexAssetContext(r, c); err != nil {
		return nil, err
	}
	fields := make(map[string]any, len(payload)+1)
	for k, v := range payload {
		fields[k] = v
	}
	// BeeNex documents project as a response/account context, not a request field.
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
	if err := beeNexProtocolError(action, resp.StatusCode, envelope, body, resp.Header, c.AccessKeyID, c.SecretAccessKey, c.SessionToken, r.SourceURL); err != nil {
		if action == "GetVisualValidateResult" {
			metadata, _ := envelope["ResponseMetadata"].(map[string]any)
			detail, _ := metadata["Error"].(map[string]any)
			token := seedanceString(fields, "BytedToken")
			if resp.StatusCode == 404 && token != "" && seedanceString(detail, "Code") == "NotFound."+token {
				return nil, seedanceError(404, "Owner verification is not yet confirmed", "verification_pending")
			}
		}
		return nil, err
	}
	result, _ := envelope["Result"].(map[string]any)
	checkProject := func(item map[string]any) error {
		project, present := item["ProjectName"]
		if c.ProjectMode == "account_scoped" && (!present || project == nil || project == "") {
			return nil
		}
		if c.ProjectMode == "account_scoped" {
			if named, ok := project.(string); ok {
				for _, allowed := range c.AccountScopedProjects {
					if allowed != "" && named == allowed {
						return nil
					}
				}
			}
		}
		if named, ok := project.(string); !ok || c.ProjectMode != "named" || named != c.ProjectName {
			return seedanceError(409, "BeeNex returned an incompatible asset project", "asset_context_incompatible")
		}
		return nil
	}
	if action == "GetAsset" || action == "GetAssetGroup" {
		if err := checkProject(result); err != nil {
			return nil, err
		}
	}
	if action == "ListAssets" || action == "ListAssetGroups" {
		items, _ := result["Items"].([]any)
		for _, item := range items {
			resource, _ := item.(map[string]any)
			if err := checkProject(resource); err != nil {
				return nil, err
			}
		}
	}
	return envelope, nil
}

func BeeNexSeedanceCapabilities() map[string]any {
	return map[string]any{
		"editing":        map[string]any{"supported": false, "reason": "No supported editing protocol is confirmed for this hy route"},
		"extension":      map[string]any{"supported": false, "reason": "No distinct extension protocol is verified for this hy route"},
		"draft":          map[string]any{"supported": false, "reason": "Draft generation and upgrade have no verified protocol for this hy route"},
		"output_formats": []string{"mp4"},
		"mov":            map[string]any{"supported": false, "reason": "Verified BeeNex output is MP4; no platform remux is provided"},
		"last_frame":     map[string]any{"supported": false, "reason": "The verified task query does not return an upstream last-frame resource"},
		"seed":           map[string]any{"accepted": true, "returned": false, "determinism_verified": false},
	}
}

// Match the existing explicitly pinned admin-generation semantics without
// enabling a disabled public ability or changing distribution configuration.
func beeNexMaterialAdminPinned(c *gin.Context, ch *model.Channel, name string) bool {
	if !IsBeeNexSeedanceModel(name) || !isBeeNexSeedanceChannel(ch) || ch.Status != common.ChannelStatusEnabled || !model.IsAdmin(c.GetInt("id")) {
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
	return seedanceChannelAllowed(c, ch, name) || beeNexMaterialAdminPinned(c, ch, name) || ctaiMaterialAdminPinned(c, ch, name)
}
