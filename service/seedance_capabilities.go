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
		if !seedanceChannelAllowed(c, ch, name) {
			continue
		}
		if specified, ok := common.GetContextKey(c, constant.ContextKeyTokenSpecificChannelId); ok && specified != strconv.Itoa(ch.Id) {
			continue
		}
		mode, configured, reason := "unverified", false, "Portrait support has not been verified for this integration"
		if isBytePlusSeedanceChannel(ch) {
			mode, reason = "official_h5", "Account entitlement requires upstream confirmation"
			_, err := loadBytePlusAssetCredentials(ch.Id)
			configured = err == nil
			if !configured {
				reason = "Separate asset API credentials are not configured"
			}
		} else if ch.Id == constant.VideoFeeSeedanceChannelID {
			mode, configured, reason = "channel_material_review", ch.Status == 1 && ch.Key != "", "Channel portrait asset review is available; this integration does not provide official owner H5 verification"
		} else if u, e := url.Parse(ch.GetBaseURL()); e == nil && (u.Hostname() == "apimart.ai" || strings.HasSuffix(u.Hostname(), ".apimart.ai") || u.Hostname() == "apib.ai" || strings.HasSuffix(u.Hostname(), ".apib.ai")) {
			mode, reason = "virtual_or_ordinary", "Material library integration exists; this integration does not expose real-person H5 verification"
		}
		result = append(result, map[string]any{"model": name, "channel_id": ch.Id, "method": mode, "configured": configured, "real_person_verified": false, "reason": reason, "hosted_callback": mode == "official_h5", "live_acceptance": "not_tested"})
	}
	return result, nil
}
