package controller

import (
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"strconv"
	"time"
)

// Admin-only, local read-only queue. It neither opens external tickets nor
// updates the receipt. Disabled historical submissions are never scanned.
func ListVideoSubmissionReviews(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil || page < 1 || page > 20000 {
		c.JSON(400, gin.H{"success": false, "message": "Invalid page"})
		return
	}
	rows, err := model.ListVideoSubmissionReviews(time.Now().Unix()-service.VideoSubmissionReviewAfterSeconds, (page-1)*50, 51)
	if err != nil {
		c.JSON(503, gin.H{"success": false, "message": "Submission review queue unavailable"})
		return
	}
	more := len(rows) > 50
	if more {
		rows = rows[:50]
	}
	items := make([]map[string]any, 0, len(rows))
	for i := range rows {
		items = append(items, service.VideoSubmissionDTO(&rows[i]))
	}
	c.JSON(200, gin.H{"success": true, "data": gin.H{"items": items, "page": page, "has_more": more, "external_tickets_created": false}})
}
