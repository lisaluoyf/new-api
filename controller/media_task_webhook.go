package controller

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

func MediaTaskCallback(c *gin.Context) {
	ctx := context.WithValue(c.Request.Context(), service.TaskWebhookContextKey, service.ValidMediaTaskWebhookToken(c.Query("apimaster_callback_token")))
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 4<<20))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "message": "failed to read body"})
		return
	}
	if err := service.ProcessMediaTaskWebhook(ctx, body); err != nil {
		logger.LogWarn(c.Request.Context(), "media task webhook ignored: "+err.Error())
		if strings.Contains(err.Error(), "invalid media task webhook payload") ||
			strings.Contains(err.Error(), "missing media task webhook id") {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false})
			return
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
