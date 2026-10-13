package controller

import (
	"context"
	"errors"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"time"
)

func VideoFeeTaskCallback(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<20)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()
	err = service.ReceiveVideoFeeWebhook(ctx, c.Request.Header, body)
	switch {
	case errors.Is(err, service.ErrVideoFeeWebhookAuth):
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false})
	case errors.Is(err, service.ErrVideoFeeWebhookPayload):
		c.JSON(http.StatusBadRequest, gin.H{"ok": false})
	case err != nil:
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false})
	default:
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}
