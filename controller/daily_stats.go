package controller

import (
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

const dailyStatsTimezoneOffsetSeconds int64 = 8 * 3600

func dailyStatsDayStart(unixSeconds int64) int64 {
	return ((unixSeconds + dailyStatsTimezoneOffsetSeconds) / 86400 * 86400) - dailyStatsTimezoneOffsetSeconds
}

func GetDailyStats(c *gin.Context) {
	now := time.Now().Unix()
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	if startTimestamp == 0 {
		startTimestamp = dailyStatsDayStart(now) - 6*86400
	}
	if endTimestamp == 0 {
		endTimestamp = dailyStatsDayStart(now)
	}
	rows, err := model.GetDailyStatsSummaries(startTimestamp, endTimestamp)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	updatedAt := int64(0)
	for _, row := range rows {
		if row.UpdatedAt > updatedAt {
			updatedAt = row.UpdatedAt
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"success":    true,
		"data":       rows,
		"updated_at": updatedAt,
	})
}
