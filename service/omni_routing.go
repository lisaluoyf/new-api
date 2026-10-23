package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"strings"
)

const omniRoutingModel = "gemini-omni-1.1-flash"

type omniRoutingRequirement struct {
	official, ext bool
	parentChannel int
}

// OmniChannelPickFilter removes incompatible routes before price/weight/affinity
// selection. No upstream task is created while choosing a compatible route.
func OmniChannelPickFilter(c *gin.Context, name string) model.ChannelPickFilter {
	if name != omniRoutingModel || c == nil || c.Request == nil {
		return nil
	}
	value, found := c.Get("omni_routing_requirement")
	var requirement omniRoutingRequirement
	if found {
		requirement = value.(omniRoutingRequirement)
	} else {
		var fields map[string]any
		if common.UnmarshalBodyReusable(c, &fields) != nil {
			return nil
		}
		for _, key := range []string{"first_frame_image", "last_frame_image", "image_with_roles", "metadata", "extend_from_task_id"} {
			if fields[key] != nil {
				requirement.official = true
			}
		}
		for _, key := range []string{"duration", "seconds", "generation_type", "nsfw_check"} {
			if _, exists := fields[key]; exists {
				requirement.ext = true
			}
		}
		if images, ok := fields["image_urls"].([]any); ok && len(images) > 1 {
			generation, _ := fields["generation_type"].(string)
			if len(images) != 3 || generation != "reference" {
				requirement.official = true
			}
		}
		if id, ok := fields["extend_from_task_id"].(string); ok && strings.TrimSpace(id) != "" {
			task, exists, err := model.GetByTaskId(c.GetInt("id"), id)
			if err != nil {
				requirement.parentChannel = -1
			} else if exists && task.Status == model.TaskStatusSuccess && task.Platform == "apimart-omni" && task.Properties.UpstreamModelName == omniRoutingModel {
				requirement.parentChannel = task.ChannelId
			}
		}
		c.Set("omni_routing_requirement", requirement)
	}
	return func(ch *model.Channel) bool {
		if ch == nil {
			return false
		}
		if requirement.parentChannel != 0 && ch.Id != requirement.parentChannel {
			return false
		}
		upstream := omniRoutingModel
		var mapping map[string]string
		if raw := ch.GetModelMapping(); raw != "" && common.UnmarshalJsonStr(raw, &mapping) == nil && mapping[omniRoutingModel] != "" {
			upstream = mapping[omniRoutingModel]
		}
		if upstream == omniRoutingModel {
			return !requirement.ext
		}
		if upstream == omniRoutingModel+"-ext" {
			return !requirement.official
		}
		return false
	}
}
func OmniChannelMatchesRequest(c *gin.Context, ch *model.Channel, name string) bool {
	filter := OmniChannelPickFilter(c, name)
	return filter == nil || filter(ch)
}
