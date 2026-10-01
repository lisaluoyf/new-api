package service

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type SeedanceGroupInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type SeedanceAssetInput struct {
	URL  string `json:"url"`
	Name string `json:"name"`
}

type SeedanceAssetSubmission struct {
	Model       string               `json:"model"`
	Group       *SeedanceGroupInput  `json:"group,omitempty"`
	GroupID     string               `json:"group_id,omitempty"`
	ProjectName string               `json:"project_name,omitempty"`
	AssetType   string               `json:"asset_type"`
	Assets      []SeedanceAssetInput `json:"assets"`
	URL         *string              `json:"url,omitempty"`
	Name        *string              `json:"name,omitempty"`
}

func ValidateSeedanceAssetSubmission(input *SeedanceAssetSubmission) error {
	if input.URL != nil {
		if len(input.Assets) > 0 {
			return seedanceError(400, "Use assets or url, not both")
		}
		item := SeedanceAssetInput{URL: *input.URL}
		if input.Name != nil {
			item.Name = *input.Name
		}
		input.Assets = []SeedanceAssetInput{item}
	}
	if input.Model == "" {
		input.Model = "seedance-2.0"
	}
	if !IsSeedanceLibraryModel(input.Model) {
		return seedanceError(400, "model must be seedance-2.0 or seedance-2.5")
	}
	if input.Group != nil && input.GroupID != "" {
		return seedanceError(400, "group and group_id cannot be used together")
	}
	if input.AssetType == "" {
		input.AssetType = "Image"
	}
	if input.AssetType != "Image" && input.AssetType != "Video" && input.AssetType != "Audio" {
		return seedanceError(400, "asset_type must be Image, Video, or Audio")
	}
	if input.ProjectName != "" && input.ProjectName != "default" {
		return seedanceError(400, "project_name must be default")
	}
	if len(input.Assets) < 1 || len(input.Assets) > 20 {
		return seedanceError(400, "assets must contain between 1 and 20 items")
	}
	for i := range input.Assets {
		asset := &input.Assets[i]
		if len(asset.URL) > 8192 {
			return seedanceError(400, fmt.Sprintf("assets[%d].url is too long", i))
		}
		parsed, err := url.Parse(asset.URL)
		if err != nil || parsed.Hostname() == "" || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			return seedanceError(400, fmt.Sprintf("assets[%d].url must be a public HTTP(S) file URL", i))
		}
		host := strings.ToLower(parsed.Hostname())
		if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || (net.ParseIP(host) != nil && (common.IsPrivateIP(net.ParseIP(host)) || net.ParseIP(host).IsUnspecified())) {
			return seedanceError(400, fmt.Sprintf("assets[%d].url must be publicly accessible", i))
		}
		if utf8.RuneCountInString(asset.Name) > 255 {
			return seedanceError(400, fmt.Sprintf("assets[%d].name is too long", i))
		}
		if asset.Name == "" {
			asset.Name = fmt.Sprintf("asset-%d", i+1)
		}
	}
	return nil
}

func CreateSeedanceGroup(c *gin.Context, name string, input SeedanceGroupInput) (*model.SeedanceResource, error) {
	if utf8.RuneCountInString(input.Name) > 255 || utf8.RuneCountInString(input.Description) > 4000 {
		return nil, seedanceError(400, "Group name or description is too long")
	}
	resource, err := selectSeedanceLibrary(c, name)
	if err != nil {
		return nil, err
	}
	resource.ID, resource.Kind, resource.Status = seedanceID("group"), "group", "Creating"
	resource.Name, resource.Description = input.Name, input.Description
	if resource.Name == "" {
		resource.Name = "My media library"
	}
	// Create the durable owner mapping before any remote resource is created.
	if err := model.DB.Create(resource).Error; err != nil {
		return nil, err
	}
	envelope, err := seedanceProviderRequest(c.Request.Context(), resource, http.MethodPost, seedanceLibraryPath+"/groups", map[string]any{"name": resource.Name, "description": resource.Description, "project_name": "default", "group_type": "AIGC"})
	if err != nil {
		model.DB.Delete(resource)
		return nil, err
	}
	resource.UpstreamID = seedanceString(seedanceResult(envelope), "Id", "id", "group_id")
	if resource.UpstreamID == "" {
		model.DB.Delete(resource)
		return nil, seedanceError(502, "Invalid media group response")
	}
	resource.Status = "Active"
	if err := model.DB.Save(resource).Error; err != nil {
		return nil, err
	}
	return resource, nil
}

func SubmitSeedanceAssets(c *gin.Context, input SeedanceAssetSubmission) (*model.SeedanceResource, error) {
	if err := ValidateSeedanceAssetSubmission(&input); err != nil {
		return nil, err
	}
	if err := ValidateSeedanceModelAccess(c, input.Model); err != nil {
		return nil, err
	}
	durations := make([]int, len(input.Assets))
	if input.AssetType == "Video" {
		for i, item := range input.Assets {
			seconds, err := ProbeRemoteVideoDurationSeconds(c.Request.Context(), item.URL)
			if err != nil {
				return nil, seedanceError(400, fmt.Sprintf("invalid_asset_material: assets[%d] video duration could not be verified", i))
			}
			maximum := 30
			if input.Model != "seedance-2.5" {
				maximum = 15
			}
			if seconds < 2 || seconds > maximum {
				return nil, seedanceError(400, fmt.Sprintf("invalid_asset_material: assets[%d] video duration must be from 2 to %d seconds", i, maximum))
			}
			durations[i] = seconds
		}
	}
	var group *model.SeedanceResource
	var err error
	if input.GroupID != "" {
		group, err = model.GetSeedanceResource(c.GetInt("id"), "group", input.GroupID)
		if err != nil {
			return nil, seedanceError(404, "Media group not found")
		}
		if group.Status != "Active" || group.UpstreamID == "" {
			return nil, seedanceError(409, "Media group is not ready")
		}
		ch, e := model.GetChannelById(group.ChannelID, true)
		if e != nil || !seedanceChannelAllowed(c, ch, input.Model) {
			return nil, seedanceError(503, "Media library is temporarily unavailable")
		}
	} else {
		groupInput := SeedanceGroupInput{}
		if input.Group != nil {
			groupInput = *input.Group
		}
		group, err = CreateSeedanceGroup(c, input.Model, groupInput)
		if err != nil {
			return nil, err
		}
	}
	task := &model.SeedanceResource{ID: seedanceID("asset_task"), Kind: "task", UserID: group.UserID, ChannelID: group.ChannelID, KeyFingerprint: group.KeyFingerprint, Model: input.Model, GroupID: group.ID, Status: "processing"}
	assetIDs := make([]string, 0, len(input.Assets))
	assets := make([]model.SeedanceResource, 0, len(input.Assets))
	for i, item := range input.Assets {
		asset := model.SeedanceResource{ID: seedanceID("asset"), Kind: "asset", UserID: group.UserID, ChannelID: group.ChannelID, KeyFingerprint: group.KeyFingerprint, Model: input.Model, GroupID: group.ID, Name: item.Name, SourceURL: item.URL, AssetType: input.AssetType, Status: "Pending", DurationSeconds: durations[i]}
		assetIDs = append(assetIDs, asset.ID)
		assets = append(assets, asset)
	}
	encoded, _ := common.Marshal(assetIDs)
	task.RequestData = string(encoded)
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(task).Error; err != nil {
			return err
		}
		return tx.Create(&assets).Error
	}); err != nil {
		return nil, err
	}
	providerAssets := make([]SeedanceAssetInput, len(input.Assets))
	for i, item := range input.Assets {
		// A unique correlation name lets us match asynchronously completed
		// results even if the provider changes their order.
		providerAssets[i] = SeedanceAssetInput{URL: item.URL, Name: assets[i].ID}
	}
	payload := map[string]any{"model": input.Model, "group_id": group.UpstreamID, "project_name": "default", "asset_type": input.AssetType, "assets": providerAssets}
	if input.Model == "doubao-seedance-2.0" {
		payload["model"] = "seedance-2.0"
	}
	envelope, err := seedanceProviderRequest(c.Request.Context(), task, http.MethodPost, seedanceLibraryPath+"/assets", payload)
	if err != nil {
		_ = model.DB.Model(task).Updates(map[string]any{"status": "failed", "fail_reason": err.Error()}).Error
		_ = model.DB.Model(&model.SeedanceResource{}).Where("user_id = ? AND id IN ?", task.UserID, assetIDs).Update("status", "Failed").Error
		return nil, err
	}
	task.UpstreamID = seedanceString(seedanceResult(envelope), "id", "task_id")
	if task.UpstreamID == "" {
		_ = model.DB.Model(task).Updates(map[string]any{"status": "failed", "fail_reason": "No media review task was returned"}).Error
		_ = model.DB.Model(&model.SeedanceResource{}).Where("user_id = ? AND id IN ?", task.UserID, assetIDs).Update("status", "Failed").Error
		return nil, seedanceError(502, "No media review task was returned")
	}
	if err := model.DB.Save(task).Error; err != nil {
		return nil, err
	}
	return task, nil
}

func SeedanceAssetDTO(resource *model.SeedanceResource) map[string]any {
	return map[string]any{"id": resource.ID, "asset_id": resource.ID, "asset_url": "asset://" + resource.ID, "name": resource.Name, "asset_type": resource.AssetType, "group_id": resource.GroupID, "status": resource.Status, "url": resource.SourceURL, "created_at": resource.CreatedAt, "updated_at": resource.UpdatedAt}
}

func SeedanceGroupDTO(resource *model.SeedanceResource) map[string]any {
	return map[string]any{"id": resource.ID, "name": resource.Name, "description": resource.Description, "created_at": resource.CreatedAt, "updated_at": resource.UpdatedAt}
}

func SeedanceTaskDTO(resource *model.SeedanceResource) map[string]any {
	data := map[string]any{"id": resource.ID, "object": "seedance.avatar.asset.task", "model": resource.Model, "status": resource.Status, "progress": resource.Progress, "group_id": resource.GroupID}
	if resource.ResultData != "" {
		var result map[string]any
		if common.UnmarshalJsonStr(resource.ResultData, &result) == nil {
			data["result"] = result
		}
	}
	if resource.Status == "failed" && resource.FailReason != "" {
		data["error"] = map[string]any{"code": "task_failed", "message": resource.FailReason}
	}
	return data
}

func PollSeedanceAssetTask(c *gin.Context, task *model.SeedanceResource) error {
	if task.Status == "completed" || task.Status == "failed" {
		return nil
	}
	envelope, err := seedanceProviderRequest(c.Request.Context(), task, http.MethodGet, "/v1/tasks/"+url.PathEscape(task.UpstreamID), nil)
	if err != nil {
		return err
	}
	data := seedanceResult(envelope)
	status := seedanceString(data, "status")
	if status != "completed" && status != "failed" {
		if n, ok := data["progress"].(float64); ok {
			task.Progress = min(99, max(0, int(n)))
		}
		return model.DB.Model(task).Update("progress", task.Progress).Error
	}
	var ids []string
	if err := common.UnmarshalJsonStr(task.RequestData, &ids); err != nil {
		return err
	}
	result, _ := data["result"].(map[string]any)
	items, _ := result["assets"].([]any)
	if len(items) == 0 && len(ids) == 1 && seedanceString(result, "asset_id") != "" {
		items = []any{result}
	}
	if status == "completed" && len(items) != len(ids) {
		return seedanceError(502, "Incomplete media review result; query the task again")
	}
	if len(items) > 0 && len(items) != len(ids) {
		return seedanceError(502, "Incomplete media review result; query the task again")
	}
	byName := map[string]map[string]any{}
	for _, value := range items {
		item, _ := value.(map[string]any)
		raw, _ := item["raw_response"].(map[string]any)
		name := seedanceString(seedanceResult(raw), "Name", "name")
		if name != "" {
			byName[name] = item
		}
	}
	if len(byName) > 0 {
		for _, id := range ids {
			if byName[id] == nil {
				return seedanceError(502, "Incomplete media review result; query the task again")
			}
		}
	}
	publicAssets, usable, failed := make([]any, 0, len(ids)), make([]any, 0), make([]any, 0)
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		for i, id := range ids {
			var asset model.SeedanceResource
			// Deleted assets must never be revived by a late review result.
			if e := tx.Where("id = ? AND user_id = ? AND kind = ?", id, task.UserID, "asset").First(&asset).Error; e != nil {
				if e == gorm.ErrRecordNotFound {
					continue
				}
				return e
			}
			asset.Status = "Failed"
			if len(items) > 0 {
				item, _ := items[i].(map[string]any)
				if len(byName) > 0 {
					item = byName[id]
				}
				asset.UpstreamID = seedanceString(item, "asset_id", "id", "Id")
				if strings.EqualFold(seedanceString(item, "status", "Status"), "Active") && asset.UpstreamID != "" {
					asset.Status = "Active"
				}
			}
			updated := tx.Model(&model.SeedanceResource{}).Where("id = ? AND user_id = ? AND kind = ?", id, task.UserID, "asset").Updates(map[string]any{"status": asset.Status, "upstream_id": asset.UpstreamID})
			if e := updated.Error; e != nil {
				return e
			}
			if updated.RowsAffected == 0 {
				continue
			}
			public := SeedanceAssetDTO(&asset)
			publicAssets = append(publicAssets, public)
			if asset.Status == "Active" {
				usable = append(usable, public)
			} else {
				failed = append(failed, public)
			}
		}
		out := map[string]any{"assets": publicAssets, "usable_assets": usable, "failed_assets": failed}
		encoded, e := common.Marshal(out)
		if e != nil {
			return e
		}
		task.Status, task.Progress, task.ResultData = status, 100, string(encoded)
		if len(failed) > 0 {
			task.Status = "failed"
		}
		if task.Status == "failed" {
			task.FailReason = "Some media assets did not pass review"
			if reason := taskErrorMessage(data, 0); reason != "" {
				for _, value := range items {
					item, _ := value.(map[string]any)
					if id := seedanceString(item, "asset_id", "id", "Id"); id != "" {
						reason = strings.ReplaceAll(reason, id, "[redacted]")
					}
				}
				task.FailReason = PublicTaskFailure(&model.Task{Status: model.TaskStatusFailure, FailReason: reason, ChannelId: task.ChannelID, PrivateData: model.TaskPrivateData{UpstreamTaskID: task.UpstreamID}})
			}
		}
		return tx.Save(task).Error
	})
	return err
}

func RefreshSeedanceAsset(c *gin.Context, asset *model.SeedanceResource) error {
	if asset.UpstreamID == "" {
		return nil
	}
	envelope, err := seedanceProviderRequest(c.Request.Context(), asset, http.MethodGet, seedanceLibraryPath+"/assets/"+url.PathEscape(asset.UpstreamID), nil)
	if err != nil {
		return err
	}
	status := seedanceString(seedanceResult(envelope), "Status", "status")
	switch strings.ToLower(status) {
	case "active":
		asset.Status = "Active"
	case "failed", "rejected":
		asset.Status = "Failed"
	default:
		asset.Status = "Pending"
	}
	return model.DB.Model(asset).Update("status", asset.Status).Error
}

func UpdateSeedanceResource(c *gin.Context, resource *model.SeedanceResource, name, description *string) error {
	payload := map[string]any{}
	if name != nil {
		if utf8.RuneCountInString(*name) > 255 {
			return seedanceError(400, "name is too long")
		}
		payload["name"] = *name
	}
	if description != nil && resource.Kind == "group" {
		if utf8.RuneCountInString(*description) > 4000 {
			return seedanceError(400, "description is too long")
		}
		payload["description"] = *description
	}
	if len(payload) == 0 {
		return seedanceError(400, "No editable fields supplied")
	}
	if resource.UpstreamID == "" {
		return seedanceError(409, "Wait for media review before updating this asset")
	}
	_, err := seedanceProviderRequest(c.Request.Context(), resource, http.MethodPatch, seedanceLibraryPath+"/"+resource.Kind+"s/"+url.PathEscape(resource.UpstreamID), payload)
	if err != nil {
		return err
	}
	return model.DB.Model(resource).Updates(payload).Error
}

func DeleteSeedanceResource(c *gin.Context, resource *model.SeedanceResource) error {
	if resource.Kind == "group" {
		var count int64
		if err := model.DB.Model(&model.SeedanceResource{}).Where("user_id = ? AND kind = ? AND group_id = ?", resource.UserID, "asset", resource.ID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return seedanceError(409, "Delete this group's assets before deleting the group")
		}
	}
	if resource.UpstreamID == "" && resource.Status == "Pending" {
		return seedanceError(409, "Wait for media review before deleting this asset")
	}
	if resource.UpstreamID != "" {
		_, err := seedanceProviderRequest(c.Request.Context(), resource, http.MethodDelete, seedanceLibraryPath+"/"+resource.Kind+"s/"+url.PathEscape(resource.UpstreamID), nil)
		if e, ok := err.(*SeedanceAPIError); err != nil && (!ok || e.Status != 404) {
			return err
		}
	}
	return model.DB.Delete(resource).Error
}
