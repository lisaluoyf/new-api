package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	byteplusbase "github.com/byteplus-sdk/byteplus-sdk-golang/base"
	"gorm.io/gorm"
)

// Keep control-plane credentials out of channel settings, client responses and Git.
// One file per channel also prevents routing another provider's AK/SK to BytePlus.
type bytePlusAssetCredentials struct {
	AccessKeyID      string `json:"access_key_id"`
	SecretAccessKey  string `json:"secret_access_key"`
	SessionToken     string `json:"session_token,omitempty"`
	Region           string `json:"region,omitempty"`
	ProjectName      string `json:"project_name,omitempty"`
	AutoRouteEnabled *bool  `json:"auto_route_enabled,omitempty"`
}

func isBytePlusSeedanceChannel(ch *model.Channel) bool {
	if ch == nil || (ch.Type != constant.ChannelTypeDoubaoVideo && ch.Type != constant.ChannelTypeVolcEngine) {
		return false
	}
	parsed, err := url.Parse(ch.GetBaseURL())
	if err != nil || parsed.Scheme != "https" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return (ch.Type == constant.ChannelTypeDoubaoVideo && strings.HasPrefix(host, "ark.") && strings.HasSuffix(host, ".bytepluses.com")) || host == "ark.cn-beijing.volces.com"
}

func loadBytePlusAssetCredentials(channelID int) (bytePlusAssetCredentials, error) {
	var result bytePlusAssetCredentials
	dir := os.Getenv("BYTEPLUS_ASSET_CREDENTIALS_DIR")
	if dir == "" {
		dir = "/run/byteplus-assets"
	}
	f, err := os.Open(filepath.Join(dir, "channel-"+strconv.Itoa(channelID)+".json"))
	if err != nil {
		return result, seedanceError(503, "Selected media library credentials are not configured", "credentials_not_configured")
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Mode().Perm()&0077 != 0 || stat.Size() > 32768 {
		return result, seedanceError(503, "Selected media library credentials are not configured")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 32769))
	if err != nil || common.Unmarshal(raw, &result) != nil || strings.TrimSpace(result.AccessKeyID) == "" || strings.TrimSpace(result.SecretAccessKey) == "" {
		return result, seedanceError(503, "Selected media library credentials are not configured")
	}
	if result.Region == "" {
		result.Region = "ap-southeast-1"
		ch, _ := model.GetChannelById(channelID, true)
		if isVolcSeedanceChannel(ch) {
			result.Region = "cn-beijing"
		}
	}
	for _, r := range result.Region {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return result, seedanceError(503, "Invalid media library region configuration")
		}
	}
	ch, _ := model.GetChannelById(channelID, true)
	if isVolcSeedanceChannel(ch) && result.Region != "cn-beijing" {
		return result, seedanceError(503, "Official Ark asset credentials require cn-beijing region")
	}
	if len(result.Region) > 64 {
		return result, seedanceError(503, "Invalid media library region configuration")
	}
	if result.ProjectName == "" {
		result.ProjectName = "default"
	}
	return result, nil
}

// Isolated for httptest; production always uses BytePlus's documented API host.
var bytePlusAssetEndpoint = func(region string) string { return "https://ark." + region + ".byteplusapi.com/" }

func bytePlusAssetRequest(ctx context.Context, resource *model.SeedanceResource, action string, payload map[string]any) (map[string]any, error) {
	credentials, err := loadBytePlusAssetCredentials(resource.ChannelID)
	if err != nil {
		return nil, err
	}
	if resource.ProjectName != "" && (resource.ProjectName != credentials.ProjectName || resource.CredentialFingerprint != seedanceControlFingerprint(credentials)) {
		return nil, seedanceError(409, "Media library account or project changed; original context is required", "asset_context_incompatible")
	}
	fields := make(map[string]any, len(payload)+1)
	for k, v := range payload {
		fields[k] = v
	}
	fields["ProjectName"] = credentials.ProjectName
	raw, err := common.Marshal(fields)
	if err != nil {
		return nil, err
	}
	host := bytePlusAssetEndpoint(credentials.Region)
	ch, _ := model.GetChannelById(resource.ChannelID, true)
	if isVolcSeedanceChannel(ch) {
		host = "https://ark.cn-beijing.volcengineapi.com/"
	}
	endpoint := host + "?" + url.Values{"Action": {action}, "Version": {"2024-01-01"}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, seedanceError(502, "Unable to contact media library")
	}
	req.Header.Set("Content-Type", "application/json")
	signing := byteplusbase.Credentials{AccessKeyID: credentials.AccessKeyID, SecretAccessKey: credentials.SecretAccessKey, SessionToken: credentials.SessionToken, Region: credentials.Region, Service: "ark"}
	signing.Sign(req)
	client := &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return nil, seedanceError(502, "Media library request failed; retry queries before submitting again")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	var envelope map[string]any
	if err != nil || len(body) > 4<<20 || common.Unmarshal(body, &envelope) != nil || envelope == nil {
		return nil, seedanceError(502, "Invalid media library response")
	}
	metadata, _ := envelope["ResponseMetadata"].(map[string]any)
	detail, failed := metadata["Error"].(map[string]any)
	if response.StatusCode < 200 || response.StatusCode >= 300 || failed || envelope["error"] != nil {
		if seedanceString(detail, "Code") == "SubscriptionRequired" {
			return nil, seedanceError(403, "Media library permissions are not enabled; contact support", "entitlement_required")
		}
		status := response.StatusCode
		if status < 400 || status == 401 || status == 403 {
			status = 502
		}
		return nil, seedanceError(status, "Media library request could not be confirmed; query existing resources before retrying")
	}
	return envelope, nil
}

func bytePlusLibraryRequest(ctx context.Context, resource *model.SeedanceResource, method, path string, payload any) (map[string]any, error) {
	fields, _ := payload.(map[string]any)
	result := func(data map[string]any) (map[string]any, error) { return map[string]any{"data": data}, nil }
	if resource.Kind == "group" {
		native := map[string]any{"Id": resource.UpstreamID}
		if name, ok := fields["name"].(string); ok {
			if utf8.RuneCountInString(name) > 64 {
				return nil, seedanceError(400, "Group name must not exceed 64 characters")
			}
			native["Name"] = name
		}
		if description, ok := fields["description"].(string); ok {
			if utf8.RuneCountInString(description) > 300 {
				return nil, seedanceError(400, "Group description must not exceed 300 characters")
			}
			native["Description"] = description
		}
		switch method {
		case http.MethodPost:
			delete(native, "Id")
			native["GroupType"] = "AIGC"
			return bytePlusAssetRequest(ctx, resource, "CreateAssetGroup", native)
		case http.MethodPatch:
			return bytePlusAssetRequest(ctx, resource, "UpdateAssetGroup", native)
		case http.MethodDelete:
			return bytePlusAssetRequest(ctx, resource, "DeleteAssetGroup", native)
		}
	}
	if resource.Kind == "task" {
		var ids []string
		if err := common.UnmarshalJsonStr(resource.RequestData, &ids); err != nil {
			return nil, err
		}
		if method == http.MethodPost && path == seedanceLibraryPath+"/assets" {
			group, err := model.GetSeedanceResource(resource.UserID, "group", resource.GroupID)
			if err != nil {
				return nil, err
			}
			for _, id := range ids {
				asset, err := model.GetSeedanceResource(resource.UserID, "asset", id)
				if err != nil {
					return nil, err
				}
				if asset.UpstreamID != "" || asset.Status != "Pending" {
					continue
				}
				if err := seedanceDB().Model(asset).Update("status", "Submitting").Error; err != nil {
					return nil, err
				}
				envelope, err := bytePlusAssetRequest(ctx, asset, "CreateAsset", map[string]any{"GroupId": group.UpstreamID, "URL": asset.SourceURL, "AssetType": asset.AssetType, "Name": asset.ID})
				if err != nil {
					return nil, err
				}
				upstream := seedanceString(seedanceResult(envelope), "Id")
				if upstream == "" {
					return nil, seedanceError(502, "Invalid media import response")
				}
				if err = seedanceDB().Model(asset).Updates(map[string]any{"upstream_id": upstream, "status": "Processing"}).Error; err != nil {
					return nil, err
				}
			}
			return result(map[string]any{"id": resource.ID})
		}
		if method == http.MethodGet {
			items := make([]any, 0, len(ids))
			terminal := 0
			for _, id := range ids {
				asset, err := model.GetSeedanceResource(resource.UserID, "asset", id)
				if errors.Is(err, gorm.ErrRecordNotFound) {
					terminal++
					items = append(items, map[string]any{"status": "Failed", "raw_response": map[string]any{"Result": map[string]any{"Name": id}}})
					continue
				}
				if err != nil {
					return nil, err
				}
				if asset.UpstreamID == "" {
					items = append(items, map[string]any{"status": "SubmissionUnknown", "raw_response": map[string]any{"Result": map[string]any{"Name": id}}})
					continue
				}
				envelope, err := bytePlusAssetRequest(ctx, asset, "GetAsset", map[string]any{"Id": asset.UpstreamID})
				if err != nil {
					return nil, err
				}
				status := seedanceString(seedanceResult(envelope), "Status")
				if e := seedanceDB().Model(asset).Updates(map[string]any{"status": status, "fail_reason": seedanceAssetReviewFailure(asset, seedanceResult(envelope))}).Error; e != nil {
					return nil, e
				}
				if status == "Active" || status == "Failed" {
					terminal++
				}
				items = append(items, map[string]any{"asset_id": asset.UpstreamID, "status": status, "raw_response": map[string]any{"Result": map[string]any{"Name": asset.ID}}})
			}
			status := "processing"
			progress := 0
			if len(ids) > 0 {
				progress = 100 * terminal / len(ids)
			}
			if terminal == len(ids) {
				status = "completed"
			}
			return result(map[string]any{"status": status, "progress": float64(progress), "result": map[string]any{"assets": items}})
		}
	}
	if resource.Kind == "asset" {
		native := map[string]any{"Id": resource.UpstreamID}
		switch method {
		case http.MethodGet:
			return bytePlusAssetRequest(ctx, resource, "GetAsset", native)
		case http.MethodPatch:
			if name, ok := fields["name"].(string); ok {
				if utf8.RuneCountInString(name) > 64 {
					return nil, seedanceError(400, "Asset name must not exceed 64 characters")
				}
				native["Name"] = name
			}
			return bytePlusAssetRequest(ctx, resource, "UpdateAsset", native)
		case http.MethodDelete:
			return bytePlusAssetRequest(ctx, resource, "DeleteAsset", native)
		}
	}
	return nil, fmt.Errorf("unsupported media library operation")
}

func seedanceControlFingerprint(c bytePlusAssetCredentials) string {
	return SeedanceKeyFingerprint(c.AccessKeyID + "\x00" + c.ProjectName)
}

func isVolcSeedanceChannel(ch *model.Channel) bool {
	if ch == nil || (ch.Type != constant.ChannelTypeDoubaoVideo && ch.Type != constant.ChannelTypeVolcEngine) {
		return false
	}
	parsed, err := url.Parse(ch.GetBaseURL())
	return err == nil && parsed.Scheme == "https" && parsed.Hostname() == "ark.cn-beijing.volces.com"
}
