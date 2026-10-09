package service

import (
	"fmt"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func seedanceRoutingVariant(c *gin.Context) (string, error) {
	fields := map[string]any{}
	if c.Request != nil && strings.Contains(c.GetHeader("Content-Type"), "multipart/form-data") {
		form, err := common.ParseMultipartFormReusable(c)
		if err != nil {
			return "", err
		}
		for key, values := range form.Value {
			if len(values) > 0 {
				fields[key] = values[0]
			}
		}
		if raw, ok := fields["metadata"].(string); ok {
			var metadata map[string]any
			if common.UnmarshalJsonStr(raw, &metadata) == nil {
				fields["metadata"] = metadata
			}
		}
	} else if c.Request != nil {
		if err := common.UnmarshalBodyReusable(c, &fields); err != nil {
			return "", err
		}
	}
	for _, key := range []string{"seedance25_normalized_request", "apimart_normalized_request"} {
		if value, exists := c.Get(key); exists {
			if normalized, ok := value.(map[string]any); ok {
				fields = normalized
				break
			}
		}
	}
	metadata, _ := fields["metadata"].(map[string]any)
	resolution, _ := fields["resolution"].(string)
	if resolution == "" {
		resolution, _ = metadata["resolution"].(string)
	}
	if resolution == "" {
		resolution, _ = fields["size"].(string)
	}
	switch strings.ToLower(strings.TrimSpace(resolution)) {
	case "", "720p", "1280x720", "720x1280":
		resolution = "720P"
	case "480p", "854x480", "480x854":
		resolution = "480P"
	case "1080p", "1920x1080", "1080x1920":
		resolution = "1080P"
	case "4k", "2160p", "3840x2160", "2160x3840":
		resolution = "4K"
	default:
		return "", fmt.Errorf("Unsupported Seedance resolution")
	}
	hasVideo, _ := metadata["has_video"].(bool)
	if len(seedanceTariffVideoURLs(fields)) > 0 || len(seedanceTariffVideoURLs(metadata)) > 0 || c.GetInt("seedance_video_input_seconds") > 0 {
		hasVideo = true
	}
	if draft, ok := c.Get("seedance_draft_task"); ok {
		source := draft.(*model.Task)
		if seedanceInt(source.PrivateData.SeedanceRequest["video_input_seconds"]) > 0 || len(seedanceTariffVideoURLs(source.PrivateData.SeedanceRequest)) > 0 {
			hasVideo = true
		}
	}
	if hasVideo {
		resolution += "-input"
	}
	return resolution, nil
}

// Snapshot restrictions once per request so retries share the same eligibility.
func SeedanceResolutionPickFilter(c *gin.Context, name string) model.ChannelPickFilter {
	name = model.NormalizeVerifiedVideoModel(name)
	if name == "" || (c.Request != nil && c.Request.Method != "POST") {
		return nil
	}
	key := "seedance_resolution_filter:" + name
	if value, exists := c.Get(key); exists {
		return value.(model.ChannelPickFilter)
	}
	variant, err := seedanceRoutingVariant(c)
	selections, dbErr := model.SeedanceResolutionSelections(name)
	filter := model.ChannelPickFilter(func(channel *model.Channel) bool {
		if channel == nil || err != nil || dbErr != nil || !slices.Contains(model.SeedanceResolutionOptions(name), variant) {
			return false
		}
		values, configured := selections[channel.Id]
		return !configured || slices.Contains(values, variant)
	})
	scope := &model.ChannelPickScope{Include: false, IDs: []int{}}
	if err != nil || dbErr != nil {
		scope.Include = true
	} else {
		for id := range selections {
			if !filter(&model.Channel{Id: id}) {
				scope.IDs = append(scope.IDs, id)
			}
		}
	}
	c.Set(key+":scope", scope)
	c.Set(key, filter)
	return filter
}

func ValidateSeedanceResolutionChannel(c *gin.Context, channel *model.Channel, name string) error {
	filter := SeedanceResolutionPickFilter(c, name)
	if filter != nil && !filter(channel) {
		return fmt.Errorf("Channel does not support the requested Seedance resolution or input type")
	}
	return nil
}

func SeedanceResolutionPickScope(c *gin.Context, name string) *model.ChannelPickScope {
	filter := SeedanceResolutionPickFilter(c, name)
	if filter == nil {
		return nil
	}
	value, _ := c.Get("seedance_resolution_filter:" + model.NormalizeVerifiedVideoModel(name) + ":scope")
	return value.(*model.ChannelPickScope)
}
