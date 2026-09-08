package controller

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

const trialLimitNotificationWalletURL = "https://apimaster.ai/console/wallet"

// RedirectTrialLimitNotification records one opaque-token click then sends the
// user to the formal APIMaster wallet URL. Invalid tokens still receive a safe
// wallet destination and reveal no notification data.
func RedirectTrialLimitNotification(c *gin.Context) {
	token := strings.TrimSpace(c.Param("token"))
	notification, _, err := model.MarkTrialLimitNotificationClicked(token)
	source := "trial_limit_telegram"
	if err == nil && notification != nil {
		if notification.Channel == model.TrialLimitNotificationDiscord {
			source = "trial_limit_discord"
		}
		if notification.Variant == model.TrialLimitNotificationFirstTopupPromo {
			source += "_first_topup"
		}
	}
	c.Redirect(http.StatusFound, trialLimitNotificationWalletURL+"?source="+source)
}

// AdminGetTrialLimitNotificationDailyMetrics returns cohort metrics for a
// Beijing calendar day. Rates use successfully sent messages as denominator.
func AdminGetTrialLimitNotificationDailyMetrics(c *gin.Context) {
	location := time.FixedZone("CST", 8*3600)
	day := strings.TrimSpace(c.DefaultQuery("day", time.Now().In(location).Format("2006-01-02")))
	parsed, err := time.ParseInLocation("2006-01-02", day, location)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "day must use YYYY-MM-DD"})
		return
	}
	start := parsed.Unix()
	end := parsed.AddDate(0, 0, 1).Unix()
	if days, _ := strconv.Atoi(c.Query("days")); days > 1 && days <= 31 {
		start = parsed.AddDate(0, 0, -(days - 1)).Unix()
	}
	metrics, err := model.GetTrialLimitNotificationDailyMetrics(start, end)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, metrics)
}
