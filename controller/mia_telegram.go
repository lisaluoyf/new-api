package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const miaDefaultChatModel = "grok-4.5"

type miaTelegramAPIKeyRequest struct {
	TelegramUserID string `json:"telegram_user_id"`
	Model          string `json:"model"`
}

// ResolveMiaTelegramAPIKey resolves an existing APIMaster user's usable token
// for the Mia bot. The route is protected by Mia's dedicated service secret.
func ResolveMiaTelegramAPIKey(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var input miaTelegramAPIKeyRequest
	if err := common.DecodeJson(c.Request.Body, &input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"code":    "invalid_request",
			"message": "invalid request",
		})
		return
	}

	telegramUserID := strings.TrimSpace(input.TelegramUserID)
	parsedTelegramID, err := strconv.ParseInt(telegramUserID, 10, 64)
	if err != nil || parsedTelegramID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"code":    "invalid_telegram_user_id",
			"message": "invalid Telegram user id",
		})
		return
	}

	user := model.User{TelegramId: telegramUserID}
	if err := user.FillUserByTelegramId(); err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"code":    "telegram_not_bound",
			"message": "Telegram account is not bound",
		})
		return
	}
	if user.Status != common.UserStatusEnabled {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"code":    "user_disabled",
			"message": "user is disabled",
		})
		return
	}

	modelName := strings.TrimSpace(input.Model)
	if modelName == "" {
		modelName = miaDefaultChatModel
	}
	token, err := model.GetUsableUserTokenForModel(user.Id, modelName)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			common.SysError("failed to resolve Mia user token: " + err.Error())
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"code":    "token_lookup_failed",
				"message": "unable to resolve API key",
			})
			return
		}
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"code":    "no_usable_api_key",
			"message": "no usable API key",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"user_id":  user.Id,
			"token_id": token.Id,
			"api_key":  token.GetFullKey(),
		},
	})
}
