package service

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// Reuse the owned original image, never another account's approval or private ID.
func seedance272OriginalImage(c *gin.Context, id string) (string, error) {
	a, err := seedanceOwnedAsset(c, id)
	if err != nil {
		return "", err
	}
	if IsSeedancePortraitAsset(a) || a.AssetType != "Image" || a.SourceURL == "" {
		return "", seedanceError(409, "Channel 272 requires an available original ordinary image; owner verification and portrait approvals cannot be transferred", "asset_context_incompatible")
	}
	input := SeedanceAssetSubmission{Assets: []SeedanceAssetInput{{URL: a.SourceURL}}}
	if err := ValidateSeedanceAssetSubmission(&input); err != nil {
		return "", seedanceError(409, "Original image is unavailable for channel 272 review", "asset_source_unavailable")
	}
	return a.SourceURL, nil
}

func seedance272TransientRoute(c *gin.Context, fields map[string]any) (*model.SeedanceResource, error) {
	selected, fixed := common.GetContextKey(c, constant.ContextKeyTokenSpecificChannelId)
	if !fixed || fmt.Sprint(selected) != "272" {
		return nil, nil
	}
	// Existing official portrait references are resolved by their original binding.
	officialPortrait := false
	_, err := visitSeedanceAssetURLs(fields, func(id string) (string, error) {
		a, e := seedanceOwnedAsset(c, id)
		if e == nil && a.ChannelID == 272 && IsSeedancePortraitAsset(a) {
			officialPortrait = true
		}
		return id, e
	})
	if err != nil {
		return nil, err
	}
	if officialPortrait {
		return nil, nil
	}
	urls := []string{}
	_, err = visitSeedanceAssetURLs(fields, func(id string) (string, error) {
		a, e := seedanceOwnedAsset(c, id)
		if e != nil {
			return "", e
		}
		// Existing official owner-verification bindings keep their original route.
		if a.ChannelID == 272 && IsSeedancePortraitAsset(a) {
			return "", seedanceError(409, "Use the original owner-verification route", "asset_context_incompatible")
		}
		source, e := seedance272OriginalImage(c, id)
		if e == nil {
			urls = append(urls, "asset://"+id)
		}
		return source, e
	})
	if err != nil || len(urls) == 0 {
		return nil, err
	}
	name, _ := fields["model"].(string)
	if err := ValidateSeedanceModelAccess(c, name); err != nil {
		return nil, err
	}
	ch, err := model.GetChannelById(272, true)
	if err != nil || !seedanceChannelAllowed(c, ch, name) || !ProviderChannelAllowed(ProviderPolicyForModel(c, name), 272) {
		return nil, seedanceError(503, "Channel 272 is unavailable for this model")
	}
	c.Set("seedance_public_asset_urls", urls)
	c.Set("seedance_unified_routing", true)
	c.Set("seedance_unified_explicit_channel", true)
	return &model.SeedanceResource{UserID: c.GetInt("id"), ChannelID: 272, Model: name, GroupType: "transient_image", Status: "Active"}, nil
}

func seedance272ManualLibraryError() error {
	return seedanceError(409, "Channel 272 supports temporary automatic review during video generation; submit manual material review to another channel", "automatic_review_only")
}
