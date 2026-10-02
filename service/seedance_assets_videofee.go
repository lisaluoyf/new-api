package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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
			path := "/api/real-person-assets/import-url"
			var payload any = map[string]any{"url": asset.SourceURL}
			// Send our own uploaded image bytes directly: VideoFee's URL importer
			// cannot fetch this origin in production. External URLs retain import.
			if upload, err := videoFeeLocalUpload(asset.SourceURL); err != nil {
				return nil, err
			} else if upload != nil {
				path = "/api/real-person-assets/upload"
				payload = *upload
			}
			imported, err := seedanceProviderRawRequest(ctx, asset, ch, key, http.MethodPost, path, payload)
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

// Only canonical filenames emitted by StoreUploadedMediaImage can access disk.
// Never download an arbitrary caller URL or interpret it as a local path.
type seedanceMediaUpload struct {
	filename string
	data     []byte
}

func videoFeeLocalUpload(source string) (*seedanceMediaUpload, error) {
	if !strings.HasPrefix(source, imageCachePublicBase) {
		return nil, nil
	}
	filename := strings.TrimPrefix(source, imageCachePublicBase)
	if !strings.HasPrefix(filename, "media_upload_task_") || filepath.Base(filename) != filename || strings.ContainsAny(filename, "%?#\\") {
		return nil, nil
	}
	file, err := os.Open(filepath.Join(imageCacheDir, filename))
	if err != nil {
		return nil, seedanceError(400, "Uploaded image is unavailable; upload it again")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (20<<20)+1))
	if err != nil {
		return nil, seedanceError(502, "Unable to read uploaded image")
	}
	if len(data) > 20<<20 {
		return nil, seedanceError(413, "This media route accepts uploads up to 20 MB")
	}
	return &seedanceMediaUpload{filename: filename, data: data}, nil
}

// Bridge APIMaster-uploaded ordinary images without requiring a separate client
// import call. Certified asset:// references have already been resolved and are
// left intact; arbitrary public URLs retain the provider's direct-URL workflow.
func ResolveVideoFeeUploadedImages(ctx context.Context, baseURL, key string, content []any) error {
	for _, value := range content {
		item, ok := value.(map[string]any)
		if !ok || item["type"] != "image_url" {
			continue
		}
		image, _ := item["image_url"].(map[string]any)
		source, _ := image["url"].(string)
		upload, err := videoFeeLocalUpload(source)
		if err != nil {
			return err
		}
		if upload == nil {
			continue
		}
		response, err := seedanceProviderRawRequest(ctx, &model.SeedanceResource{}, &model.Channel{BaseURL: &baseURL, Key: key}, key, http.MethodPost, "/api/assets/upload", *upload)
		if err != nil {
			return err
		}
		row, _ := response["row"].(map[string]any)
		id := seedanceString(row, "assetKey")
		if id == "" {
			return seedanceError(502, "Invalid media upload response")
		}
		image["url"] = "asset://" + id
	}
	return nil
}
