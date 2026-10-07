package middleware

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func seedanceRoutingError(c *gin.Context, err error) {
	status := 400
	if e, ok := err.(*service.SeedanceAPIError); ok {
		status = e.Status
	}
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"message": err.Error(), "type": "invalid_request_error"}})
}

func PrepareSeedanceAssetGeneration() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodPost || !strings.Contains(c.GetHeader("Content-Type"), "application/json") || (!strings.HasSuffix(c.Request.URL.Path, "/generations") && c.Request.URL.Path != "/v1/videos" && c.Request.URL.Path != "/v1/videos/quote") {
			c.Next()
			return
		}
		fields, err := service.SeedanceRequestFields(c)
		if err != nil {
			seedanceRoutingError(c, fmt.Errorf("Invalid generation JSON"))
			return
		}
		if fields["model"] != "seedance-2.5" && (fields["draft"] == true || fields["draft_task_id"] != nil) {
			seedanceRoutingError(c, fmt.Errorf("Draft mode is only supported by seedance-2.5"))
			return
		}
		if fields["model"] == "seedance-2.5" {
			fields, err = service.NormalizeSeedance25Generation(c, fields)
			if err != nil {
				seedanceRoutingError(c, err)
				return
			}
			if err = service.ValidateSeedanceVideoInputs(c, fields); err != nil {
				seedanceRoutingError(c, err)
				return
			}
			if v, ok := c.Get("seedance_draft_task"); ok {
				task := v.(*model.Task)
				pin := strconv.Itoa(task.ChannelId)
				if specified, exists := common.GetContextKey(c, constant.ContextKeyTokenSpecificChannelId); exists && specified != pin {
					seedanceRoutingError(c, fmt.Errorf("Draft is temporarily unavailable; retry with the same draft_task_id later"))
					return
				}
				common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, pin)
			}
		}
		if name, _ := fields["model"].(string); service.IsSeedance20Variant(name) || name == "seedance-2.0" {
			if err = service.ValidateSeedanceVideoInputs(c, fields); err != nil {
				seedanceRoutingError(c, err)
				return
			}
		}
		asset, err := service.SeedanceAssetRouting(c, fields)
		if err != nil {
			seedanceRoutingError(c, err)
			return
		}
		if asset != nil {
			pin := strconv.Itoa(asset.ChannelID)
			if specified, ok := common.GetContextKey(c, constant.ContextKeyTokenSpecificChannelId); ok && specified != pin {
				seedanceRoutingError(c, fmt.Errorf("Selected media library is unavailable for this key"))
				return
			}
			common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, pin)
			c.Set("seedance_asset_key_fingerprint", asset.KeyFingerprint)
			if service.IsSeedancePortraitAsset(asset) && c.Request.URL.Path != "/v1/videos/quote" {
				receipt, fresh, e := service.BeginSeedancePortraitVideo(c, fields, asset)
				if e != nil {
					seedanceRoutingError(c, e)
					return
				}
				c.Header("X-Seedance-Request-Id", receipt.ID)
				if !fresh {
					c.AbortWithStatusJSON(202, gin.H{"code": 202, "data": service.SeedanceVideoRequestDTO(receipt)})
					return
				}
				c.Set("seedance_portrait_video_receipt", receipt)
			}
		}
		c.Next()
	}
}

func ApplySeedanceAssetKey() gin.HandlerFunc {
	return func(c *gin.Context) {
		fingerprint := c.GetString("seedance_draft_key_fingerprint")
		if fingerprint == "" {
			fingerprint = c.GetString("seedance_asset_key_fingerprint")
		}
		if fingerprint != "" {
			ch, err := model.GetChannelById(common.GetContextKeyInt(c, constant.ContextKeyChannelId), true)
			if err != nil || ch == nil {
				seedanceRoutingError(c, fmt.Errorf("Media library is temporarily unavailable"))
				return
			}
			key, index, err := service.SeedanceResourceKey(ch, fingerprint)
			if err != nil {
				seedanceRoutingError(c, err)
				return
			}
			common.SetContextKey(c, constant.ContextKeyChannelKey, key)
			common.SetContextKey(c, constant.ContextKeyChannelMultiKeyIndex, index)
		}
		c.Next()
	}
}

// Review tasks have their own owner-checked mapping and never need image-model
// routing, available wallet balance, or a guessed provider task identifier.
func DistributeUnlessSeedanceAssetTask() gin.HandlerFunc {
	distribute := Distribute()
	return func(c *gin.Context) {
		if strings.HasPrefix(c.Param("task_id"), "asset_task_") {
			c.Next()
			return
		}
		if _, found, err := model.GetByTaskId(c.GetInt("id"), c.Param("task_id")); err == nil && found {
			c.Next()
			return
		}
		distribute(c)
	}
}
