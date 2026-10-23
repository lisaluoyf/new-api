package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"net/url"
	"strconv"
	"strings"
)

// Read local configuration only. Entitlements are not inferred from configured keys.
func SeedancePortraitCapabilities(c *gin.Context, name string) ([]map[string]any, error) {
	if name != "seedance-2.0" && name != "seedance-2.0-fast" && name != "seedance-2.0-mini" && name != "seedance-2.5" {
		return nil, seedanceError(400, "Real-person capabilities require a verified public model ID")
	}
	if err := ValidateSeedanceModelAccess(c, name); err != nil {
		return nil, err
	}
	var channels []*model.Channel
	if err := model.DB.Find(&channels).Error; err != nil {
		return nil, err
	}
	result := []map[string]any{}
	for _, ch := range channels {
		if !seedanceLibraryChannelAllowed(c, ch, name) {
			continue
		}
		if specified, ok := common.GetContextKey(c, constant.ContextKeyTokenSpecificChannelId); ok && specified != strconv.Itoa(ch.Id) {
			continue
		}
		mode, configured, reason := "unverified", false, "Portrait support has not been verified for this integration"
		if isBeeNexSeedanceChannel(ch) && IsBeeNexSeedanceModel(name) {
			mode, reason = "virtual_or_ordinary", "BeeNex AIGC asset upload/review is separate from owner or live-person verification"
			_, err := loadBeeNexAssetCredentials(ch.Id)
			configured = err == nil
			if !configured {
				reason = "Separate BeeNex asset signing credentials, region and project are not configured"
			}
		} else if isBytePlusSeedanceChannel(ch) {
			mode, reason = "official_h5", "Account entitlement requires upstream confirmation"
			_, err := loadBytePlusAssetCredentials(ch.Id)
			configured = err == nil
			if !configured {
				reason = "Separate asset API credentials are not configured"
			}
		} else if isTokenHubSeedanceChannel(ch) {
			mode, reason = "virtual_or_ordinary", "TokenHub supports AIGC material groups and review; this does not establish real-person H5 verification or moderation-strategy effectiveness"
			_, err := loadTokenHubAssetCredentials(ch.Id)
			configured = err == nil
			if !configured {
				reason = "Separate TokenHub material credentials are not configured"
			}
		} else if isXingchiyunSeedanceChannel(ch) {
			mode, configured, reason = "official_h5", ch.Key != "", "Gateway provides official H5 sessions and server-side result polling; complete owner verification before importing real-person assets"
		} else if ch.Id == constant.VideoFeeSeedanceChannelID {
			mode, configured, reason = "channel_material_review", ch.Status == 1 && ch.Key != "", "Channel portrait asset review is available; this integration does not provide official owner H5 verification"
		} else if u, e := url.Parse(ch.GetBaseURL()); e == nil && (u.Hostname() == "apimart.ai" || strings.HasSuffix(u.Hostname(), ".apimart.ai") || u.Hostname() == "apib.ai" || strings.HasSuffix(u.Hostname(), ".apib.ai")) {
			mode, reason = "virtual_or_ordinary", "Material library integration exists; this integration does not expose real-person H5 verification"
		}
		entry := map[string]any{"model": name, "channel_id": ch.Id, "method": mode, "configured": configured, "real_person_verified": false, "reason": reason, "hosted_callback": mode == "official_h5" && !isXingchiyunSeedanceChannel(ch), "live_acceptance": "not_tested"}
		if isTokenHubSeedanceChannel(ch) {
			entry["material_capabilities"] = map[string]any{"ordinary_or_virtual": true, "upstream_group_type": "AIGC", "upload": true, "review": true, "crud": true, "official_h5": false, "liveness": false, "generation_reference_acceptance": "not_tested", "moderation_strategy_passthrough": []string{"Skip"}, "moderation_strategy_effect": "not_confirmed_by_provider"}
			entry["generation_route_enabled"] = seedanceChannelAllowed(c, ch, name)
			if credentials, err := loadTokenHubAssetCredentials(ch.Id); err == nil {
				var evidence struct{ VerifiedAt int64 }
				if err := model.DB.Model(&model.SeedanceResource{}).Select("COALESCE(MAX(updated_at), 0) AS verified_at").Where("channel_id = ? AND model = ? AND kind = ? AND status = ? AND credential_fingerprint = ?", ch.Id, name, "asset", "Active", tokenHubCredentialFingerprint(credentials)).Scan(&evidence).Error; err == nil && evidence.VerifiedAt > 0 {
					entry["live_acceptance"] = "ordinary_asset_lifecycle_observed"
					entry["material_verified_at"] = evidence.VerifiedAt
				}
			}
		}
		if isBeeNexSeedanceChannel(ch) && IsBeeNexSeedanceModel(name) {
			if credentials, err := loadBeeNexAssetCredentials(ch.Id); err == nil {
				if credentials.OfficialH5Enabled {
					entry["method"], entry["hosted_callback"] = "official_h5", false
					entry["reason"] = "Official H5 session creation and server-side result polling are configured; complete owner verification before using real-person materials"
				}
				entry["project_scope"] = credentials.ProjectMode
				entry["project_name_available"] = credentials.ProjectMode == "named"
				if at := beeNexMaterialVerifiedAt(credentials, name); at > 0 {
					entry["live_acceptance"] = "ordinary_asset_lifecycle_verified"
					entry["material_verified_at"] = at
				}
				generationReference := "not_tested"
				if credentials.MaterialVerifiedFingerprint == beeNexControlFingerprint(credentials) && credentials.GenerationReferenceVerifiedModels[name] > 0 {
					generationReference = "verified"
				}
				entry["material_capabilities"] = map[string]any{
					"ordinary_or_virtual": true, "upstream_group_type": "AIGC",
					"upload": true, "review": true, "preview": true, "crud": true,
					"generation_reference_acceptance": generationReference,
					"official_h5":                     credentials.OfficialH5Enabled, "liveness": credentials.OfficialH5Enabled,
				}
			}
			entry["generation_capabilities"] = BeeNexSeedanceCapabilities()
			entry["generation_route_enabled"] = seedanceChannelAllowed(c, ch, name)
			protocol, err := BeeNexTaskQueryProtocol(ch, name)
			if err == nil {
				entry["task_query_protocol"] = protocol
			}
		}
		result = append(result, entry)
	}
	return result, nil
}
