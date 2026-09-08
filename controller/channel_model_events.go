package controller

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func GetChannelModelEvents(c *gin.Context) {
	channelID, err := strconv.Atoi(c.Query("channel_id"))
	modelName := strings.TrimSpace(c.Query("model"))
	if err != nil || channelID <= 0 || modelName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "channel_id and model are required"})
		return
	}
	query := model.DB.Where("channel_id = ? AND model IN ?", channelID, service.ModelNameCandidates(modelName))
	if raw := c.Query("before_id"); raw != "" {
		before, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || before <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid before_id"})
			return
		}
		query = query.Where("id < ?", before)
	}
	events := make([]model.ChannelModelEvent, 0)
	if err := query.Order("id DESC").Limit(51).Find(&events).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Unable to load model status history"})
		return
	}
	hasMore := len(events) > 50
	if hasMore {
		events = events[:50]
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": events, "has_more": hasMore})
}
