package service

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// Visit only URL strings beginning with asset://; ordinary URLs and explicit
// false/zero values remain unchanged. Both arrays and image-role objects work.
func visitSeedanceAssetURLs(value any, visit func(string) (string, error)) (any, error) {
	switch v := value.(type) {
	case string:
		if strings.HasPrefix(v, "asset://") {
			return visit(strings.TrimPrefix(v, "asset://"))
		}
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			next, err := visitSeedanceAssetURLs(child, visit)
			if err != nil {
				return nil, err
			}
			out[i] = next
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, child := range v {
			next, err := visitSeedanceAssetURLs(child, visit)
			if err != nil {
				return nil, err
			}
			out[key] = next
		}
		return out, nil
	}
	return value, nil
}

func seedanceOwnedAsset(c *gin.Context, id string) (*model.SeedanceResource, error) {
	asset, err := model.GetSeedanceResource(c.GetInt("id"), "asset", id)
	if err != nil {
		return nil, seedanceError(400, "Referenced asset was not found in your media library")
	}
	if asset.Status != "Active" || asset.UpstreamID == "" {
		return nil, seedanceError(409, "Referenced asset has not passed review", "asset_not_active")
	}
	if asset.GroupType == "real_person" {
		group, e := model.GetSeedanceResource(asset.UserID, "group", asset.GroupID)
		if e != nil || group.VerificationID == "" {
			return nil, seedanceError(409, "Owner verification is not confirmed")
		}
		verification, e := model.GetSeedanceResource(asset.UserID, "verification", group.VerificationID)
		if e != nil || verification.Status != "completed" || verification.GroupID != group.ID {
			return nil, seedanceError(409, "Owner verification is not confirmed")
		}
	}
	return asset, nil
}

func SeedanceAssetRouting(c *gin.Context, fields map[string]any) (*model.SeedanceResource, error) {
	if asset, err := chooseUnifiedSeedanceRoute(c, fields, nil); err != nil || asset != nil {
		return asset, err
	}
	var pinned *model.SeedanceResource
	publicURLs := []string{}
	seen := map[string]bool{}
	_, err := visitSeedanceAssetURLs(fields, func(id string) (string, error) {
		asset, err := seedanceOwnedAsset(c, id)
		if err != nil {
			return "", err
		}
		if asset.GroupType == "channel_portrait" && fields["model"] != asset.Model {
			return "", seedanceError(409, "Channel portrait certification compatibility with the requested model is not confirmed", "asset_context_incompatible")
		}
		if asset.ChannelID == constant.BeeNexSeedanceChannelID && fields["model"] != asset.Model {
			return "", seedanceError(409, "Use a BeeNex material registered for the requested model; cross-model generation is not verified", "asset_context_incompatible")
		}
		if pinned != nil && (pinned.ChannelID != asset.ChannelID || pinned.KeyFingerprint != asset.KeyFingerprint || pinned.ProjectName != asset.ProjectName || pinned.CredentialFingerprint != asset.CredentialFingerprint) {
			return "", seedanceError(400, "All referenced assets must belong to the same media library")
		}
		if pinned == nil || IsSeedancePortraitAsset(asset) {
			pinned = asset
		}
		if !seen[id] {
			publicURLs = append(publicURLs, "asset://"+id)
			seen[id] = true
		}
		return "asset://" + id, nil
	})
	if err != nil || pinned == nil {
		return pinned, err
	}
	name, _ := fields["model"].(string)
	if err := ValidateSeedanceModelAccess(c, name); err != nil {
		return nil, err
	}
	ch, err := model.GetChannelById(pinned.ChannelID, true)
	if err != nil || !seedanceLibraryChannelAllowed(c, ch, name) {
		return nil, seedanceError(503, "Media library is temporarily unavailable")
	}
	if _, _, err := SeedanceResourceKey(ch, pinned.KeyFingerprint); err != nil {
		return nil, err
	}
	if pinned.ProjectName != "" && isBytePlusSeedanceChannel(ch) {
		credentials, e := loadBytePlusAssetCredentials(ch.Id)
		if e != nil || credentials.ProjectName != pinned.ProjectName || seedanceControlFingerprint(credentials) != pinned.CredentialFingerprint {
			return nil, seedanceError(409, "Asset account or project is incompatible with this route")
		}
	}
	if isBeeNexSeedanceChannel(ch) && IsBeeNexSeedanceModel(name) {
		credentials, e := loadBeeNexAssetCredentials(ch.Id)
		if e != nil || validateBeeNexAssetContext(pinned, credentials) != nil {
			return nil, seedanceError(409, "BeeNex asset account or project changed", "asset_context_incompatible")
		}
	}
	c.Set("seedance_public_asset_urls", publicURLs)
	return pinned, nil
}

// Resolve only in the outbound request. Logs and stored public requests retain
// APIMaster asset IDs, rather than the shared provider's private identifiers.
func ResolveSeedanceAssetReferences(c *gin.Context, fields map[string]any, channelID int, key string) (map[string]any, error) {
	value, err := visitSeedanceAssetURLs(fields, func(id string) (string, error) {
		name := common.GetContextKeyString(c, constant.ContextKeyOriginalModel)
		if name == "" {
			original, _ := fields["model"].(string)
			name = model.NormalizeVerifiedVideoModel(original)
		}
		asset, err := unifiedSeedanceCopy(c, id, name, channelID)
		if err != nil {
			return "", err
		}
		if asset.Status != "Active" || asset.UpstreamID == "" {
			return "", fmt.Errorf("Material is not ready")
		}
		if asset.ChannelID != channelID || asset.KeyFingerprint != SeedanceKeyFingerprint(key) {
			return "", fmt.Errorf("Media library is temporarily unavailable")
		}
		if channelID == constant.BeeNexSeedanceChannelID && fields["model"] != asset.Model {
			return "", seedanceError(409, "BeeNex material model context does not match", "asset_context_incompatible")
		}
		if channelID == constant.BeeNexSeedanceChannelID && IsBeeNexSeedanceModel(common.Interface2String(fields["model"])) {
			credentials, err := loadBeeNexAssetCredentials(channelID)
			if err != nil {
				return "", err
			}
			if err := validateBeeNexAssetContext(asset, credentials); err != nil {
				return "", err
			}
		}
		return "asset://" + asset.UpstreamID, nil
	})
	if err != nil {
		return nil, err
	}
	result, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Invalid media reference request")
	}
	return result, nil
}

func SeedanceRequestFields(c *gin.Context) (map[string]any, error) {
	var fields map[string]any
	if err := common.UnmarshalBodyReusable(c, &fields); err != nil {
		return nil, err
	}
	return fields, nil
}

// Channel material review is distinct from official owner verification, but
// both require durable generation deduplication and a compatible pinned route.
func IsSeedancePortraitAsset(asset *model.SeedanceResource) bool {
	return asset.GroupType == "real_person" || asset.GroupType == "channel_portrait"
}
