package service

import (
	"context"
	"strings"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

// Recovery only queries the original account and group. It never creates media
// or retries a video. List status is not authoritative; verify the exact ID by GET.
func recoverBeeNexAsset(ctx context.Context, asset *model.SeedanceResource) error {
	if asset.ChannelID != constant.BeeNexSeedanceChannelID || asset.UpstreamID != "" {
		return nil
	}
	credentials, err := loadBeeNexAssetCredentials(asset.ChannelID)
	if err != nil {
		return err
	}
	if err := validateBeeNexAssetContext(asset, credentials); err != nil {
		return err
	}
	group, err := model.GetSeedanceResource(asset.UserID, "group", asset.GroupID)
	if err != nil || group.UpstreamID == "" || group.ChannelID != asset.ChannelID || group.KeyFingerprint != asset.KeyFingerprint || validateBeeNexAssetContext(group, credentials) != nil {
		return seedanceError(409, "The original material group is unavailable", "asset_context_incompatible")
	}
	groupType := "AIGC"
	if group.GroupType == "real_person" {
		groupType = "LivenessFace"
	}
	envelope, err := beeNexAssetRequest(ctx, asset, "ListAssets", map[string]any{"Filter": map[string]any{"Name": asset.ID, "GroupIds": []string{group.UpstreamID}, "GroupType": groupType}, "PageNumber": 1, "PageSize": 100})
	if err != nil {
		return err
	}
	result := seedanceResult(envelope)
	items, ok := result["Items"].([]any)
	if !ok || len(items) >= 100 {
		return seedanceError(409, "Material submission cannot be uniquely confirmed", "material_submission_unknown")
	}
	if count, ok := result["TotalCount"].(float64); ok && count > float64(len(items)) {
		return seedanceError(409, "Material submission cannot be uniquely confirmed", "material_submission_unknown")
	}
	upstreamID := ""
	for _, value := range items {
		item, _ := value.(map[string]any)
		if seedanceString(item, "Name") != asset.ID || seedanceString(item, "GroupId") != group.UpstreamID || seedanceString(item, "AssetType") != asset.AssetType {
			continue
		}
		id := seedanceString(item, "Id")
		if !strings.HasPrefix(strings.ToLower(id), "asset-") || upstreamID != "" {
			return seedanceError(409, "Material submission cannot be uniquely confirmed", "material_submission_unknown")
		}
		upstreamID = id
	}
	if upstreamID == "" {
		return seedanceError(409, "Material submission remains unconfirmed; no upload was repeated", "material_submission_unknown")
	}
	envelope, err = beeNexAssetRequest(ctx, asset, "GetAsset", map[string]any{"Id": upstreamID})
	if err != nil {
		return err
	}
	item := seedanceResult(envelope)
	if seedanceString(item, "Id") != upstreamID || seedanceString(item, "Name") != asset.ID || seedanceString(item, "GroupId") != group.UpstreamID || seedanceString(item, "AssetType") != asset.AssetType {
		return seedanceError(409, "Recovered material does not match its original submission", "asset_context_incompatible")
	}
	status := seedanceString(item, "Status")
	if status != "Active" && status != "Processing" && status != "Failed" {
		return seedanceError(502, "Recovered material has an unverified review status", "material_submission_unknown")
	}
	preview := seedanceGatewayPreviewData(asset, item)
	failure := seedanceAssetReviewFailure(asset, item)
	update := seedanceDB().Model(&model.SeedanceResource{}).Where("id = ? AND user_id = ? AND channel_id = ? AND kind = ? AND upstream_id = ? AND credential_fingerprint = ?", asset.ID, asset.UserID, asset.ChannelID, "asset", "", asset.CredentialFingerprint).Updates(map[string]any{"upstream_id": upstreamID, "status": status, "result_data": preview, "fail_reason": failure})
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		current, err := model.GetSeedanceResource(asset.UserID, "asset", asset.ID)
		if err != nil || current.UpstreamID != upstreamID || current.CredentialFingerprint != asset.CredentialFingerprint {
			return seedanceError(409, "Material context changed during recovery", "asset_context_incompatible")
		}
		*asset = *current
		return nil
	}
	asset.UpstreamID, asset.Status, asset.ResultData, asset.FailReason = upstreamID, status, preview, failure
	return nil
}
