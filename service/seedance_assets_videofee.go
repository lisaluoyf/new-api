package service

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// VideoFee has no folders or batch certification tasks. APIMaster owns the
// folder and batch IDs and maps each asset to an independent provider review.
func videoFeeLibraryRequest(ctx context.Context, resource *model.SeedanceResource, ch *model.Channel, key, method, path string, payload any) (map[string]any, error) {
	result := func(data map[string]any) (map[string]any, error) { return map[string]any{"data": data}, nil }
	if resource.Kind == "group" {
		switch method {
		case http.MethodPost:
			return result(map[string]any{"id": resource.ID})
		case http.MethodPatch, http.MethodDelete:
			return result(map[string]any{})
		}
	}
	if method == http.MethodPost && path == seedanceLibraryPath+"/assets" {
		var ids []string
		if err := common.UnmarshalJsonStr(resource.RequestData, &ids); err != nil {
			return nil, err
		}
		// Keep each imported identifier even if a later import in the batch fails.
		for _, id := range ids {
			asset, err := model.GetSeedanceResource(resource.UserID, "asset", id)
			if err != nil {
				return nil, err
			}
			imported, err := seedanceProviderRawRequest(ctx, asset, ch, key, http.MethodPost, "/api/real-person-assets/import-url", map[string]any{"url": asset.SourceURL})
			if err != nil {
				return nil, err
			}
			row, _ := imported["row"].(map[string]any)
			upstream := seedanceString(row, "assetKey")
			if upstream == "" {
				return nil, seedanceError(502, "Invalid media import response")
			}
			if err = model.DB.Model(asset).Update("upstream_id", upstream).Error; err != nil {
				return nil, err
			}
		}
		return result(map[string]any{"id": resource.ID})
	}
	if resource.Kind == "task" && method == http.MethodGet {
		var ids []string
		if err := common.UnmarshalJsonStr(resource.RequestData, &ids); err != nil {
			return nil, err
		}
		items := make([]any, 0, len(ids))
		terminal := 0
		for _, id := range ids {
			asset, err := model.GetSeedanceResource(resource.UserID, "asset", id)
			if err != nil {
				return nil, err
			}
			if asset.UpstreamID == "" {
				return nil, seedanceError(502, "Media import is incomplete")
			}
			status, err := videoFeeAssetStatus(ctx, asset, ch, key)
			if err != nil {
				return nil, err
			}
			if status == "Active" || status == "Failed" {
				terminal++
			}
			items = append(items, map[string]any{"asset_id": asset.UpstreamID, "status": status, "raw_response": map[string]any{"Result": map[string]any{"Name": asset.ID}}})
		}
		status := "processing"
		if terminal == len(ids) {
			status = "completed"
		}
		progress := float64(0)
		if len(ids) > 0 {
			progress = float64(100 * terminal / len(ids))
		}
		return result(map[string]any{"status": status, "progress": progress, "result": map[string]any{"assets": items}})
	}
	if resource.Kind == "asset" {
		switch method {
		case http.MethodGet:
			status, err := videoFeeAssetStatus(ctx, resource, ch, key)
			if err != nil {
				return nil, err
			}
			return result(map[string]any{"status": status})
		case http.MethodPatch, http.MethodDelete:
			// These manage the APIMaster entry. VideoFee does not document remote
			// rename/delete endpoints, so retain the upstream file instead of guessing.
			return result(map[string]any{})
		}
	}
	return nil, fmt.Errorf("unsupported media library operation")
}

func videoFeeAssetStatus(ctx context.Context, asset *model.SeedanceResource, ch *model.Channel, key string) (string, error) {
	response, err := seedanceProviderRawRequest(ctx, asset, ch, key, http.MethodGet, "/api/assets/"+url.PathEscape(asset.UpstreamID), nil)
	if err != nil {
		return "", err
	}
	row, _ := response["row"].(map[string]any)
	status := seedanceString(row, "status")
	if status == "" {
		cert, _ := row["assetCertification"].(map[string]any)
		status = seedanceString(cert, "status")
	}
	switch strings.ToLower(status) {
	case "active":
		return "Active", nil
	case "failed", "blocked", "rejected":
		return "Failed", nil
	case "certifying", "pending", "uploaded", "processing":
		return "Pending", nil
	default:
		return "", seedanceError(502, "Invalid media certification status")
	}
}
