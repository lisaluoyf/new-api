package service

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
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
		return nil, seedanceError(400, "Referenced asset has not passed review")
	}
	return asset, nil
}

func SeedanceAssetRouting(c *gin.Context, fields map[string]any) (*model.SeedanceResource, error) {
	var pinned *model.SeedanceResource
	_, err := visitSeedanceAssetURLs(fields, func(id string) (string, error) {
		asset, err := seedanceOwnedAsset(c, id)
		if err != nil {
			return "", err
		}
		if pinned != nil && (pinned.ChannelID != asset.ChannelID || pinned.KeyFingerprint != asset.KeyFingerprint) {
			return "", seedanceError(400, "All referenced assets must belong to the same media library")
		}
		pinned = asset
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
	if err != nil || !seedanceChannelAllowed(c, ch, name) {
		return nil, seedanceError(503, "Media library is temporarily unavailable")
	}
	if _, _, err := SeedanceResourceKey(ch, pinned.KeyFingerprint); err != nil {
		return nil, err
	}
	return pinned, nil
}

// Resolve only in the outbound request. Logs and stored public requests retain
// APIMaster asset IDs, rather than the shared provider's private identifiers.
func ResolveSeedanceAssetReferences(c *gin.Context, fields map[string]any, channelID int, key string) (map[string]any, error) {
	value, err := visitSeedanceAssetURLs(fields, func(id string) (string, error) {
		asset, err := seedanceOwnedAsset(c, id)
		if err != nil {
			return "", err
		}
		if asset.ChannelID != channelID || asset.KeyFingerprint != SeedanceKeyFingerprint(key) {
			return "", fmt.Errorf("Media library is temporarily unavailable")
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
