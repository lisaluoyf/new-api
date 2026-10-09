package controller

import (
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"net/http"
)

func SaveSeedanceChannelResolutions(c *gin.Context) {
	var request struct {
		ChannelID   int      `json:"channel_id"`
		Model       string   `json:"model"`
		Resolutions []string `json:"resolutions"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid resolution selection"})
		return
	}
	channel, err := model.GetChannelById(request.ChannelID, false)
	if err != nil || !seedanceChannelSupportsModel(channel, request.Model) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Channel does not support this model"})
		return
	}
	if err := model.SaveSeedanceResolutionSelection(request.ChannelID, request.Model, request.Resolutions); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func seedanceChannelSupportsModel(channel *model.Channel, name string) bool {
	for _, value := range channel.GetModels() {
		if model.NormalizeVerifiedVideoModel(value) == model.NormalizeVerifiedVideoModel(name) && model.NormalizeVerifiedVideoModel(name) != "" {
			return true
		}
	}
	return false
}
