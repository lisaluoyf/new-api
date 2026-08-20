package controller

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const telegramWebhookSecretHeader = "X-Telegram-Bot-Api-Secret-Token"

type telegramWebhookUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		Chat struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		} `json:"chat"`
		From *struct {
			ID int64 `json:"id"`
		} `json:"from"`
		Text string `json:"text"`
	} `json:"message"`
}

type telegramSendMessageRequest struct {
	ChatID      int64                  `json:"chat_id"`
	Text        string                 `json:"text"`
	ReplyMarkup telegramInlineKeyboard `json:"reply_markup"`
}

type telegramSetWebhookRequest struct {
	URL                string   `json:"url"`
	SecretToken        string   `json:"secret_token"`
	AllowedUpdates     []string `json:"allowed_updates"`
	DropPendingUpdates bool     `json:"drop_pending_updates"`
}

type telegramWebhookSetupInput struct {
	DropPendingUpdates bool `json:"drop_pending_updates"`
}

type telegramInlineKeyboard struct {
	InlineKeyboard [][]telegramInlineButton `json:"inline_keyboard"`
}

type telegramInlineButton struct {
	Text string `json:"text"`
	URL  string `json:"url"`
}

func telegramGroupVerificationConfigured() bool {
	return strings.TrimSpace(common.TelegramBotToken) != "" &&
		strings.TrimSpace(common.TelegramBotName) != "" &&
		strings.TrimSpace(common.TelegramGroupChatID) != "" &&
		strings.TrimSpace(common.TelegramGroupURL) != "" &&
		isValidTelegramWebhookSecret(strings.TrimSpace(common.TelegramWebhookSecret))
}

func isValidTelegramWebhookSecret(secret string) bool {
	if len(secret) < 32 || len(secret) > 256 {
		return false
	}
	for _, char := range secret {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9' || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func SetupTelegramWebhook(c *gin.Context) {
	if !telegramGroupVerificationConfigured() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "Telegram community verification is not configured"})
		return
	}
	var input telegramWebhookSetupInput
	if c.Request.ContentLength != 0 {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
		if err := common.DecodeJson(c.Request.Body, &input); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid request"})
			return
		}
	}
	webhookURL := strings.TrimRight(strings.TrimSpace(system_setting.ServerAddress), "/") + "/api/telegram/webhook"
	parsedURL, err := url.Parse(webhookURL)
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Server address must be a public HTTPS URL"})
		return
	}
	payload, err := common.Marshal(telegramSetWebhookRequest{
		URL:                webhookURL,
		SecretToken:        common.TelegramWebhookSecret,
		AllowedUpdates:     []string{"message"},
		DropPendingUpdates: input.DropPendingUpdates,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Unable to prepare Telegram webhook"})
		return
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, telegramBotAPIURL("setWebhook"), strings.NewReader(string(payload)))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Unable to prepare Telegram webhook"})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := telegramHTTPClient.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "Unable to reach Telegram"})
		return
	}
	defer resp.Body.Close()
	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := common.DecodeJson(resp.Body, &result); err != nil || !result.OK {
		message := result.Description
		if message == "" {
			message = "Telegram rejected the webhook configuration"
		}
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": message})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"url": webhookURL}})
}

func StartTelegramGroupVerification(c *gin.Context) {
	if !telegramGroupVerificationConfigured() {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "Telegram community verification is not configured",
		})
		return
	}

	userID := c.GetInt("id")
	verification, token, err := model.StartTelegramGroupVerification(userID, time.Now())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Unable to start Telegram verification"})
		return
	}
	if verification.TelegramID != nil && strings.TrimSpace(*verification.TelegramID) != "" {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"identified": true,
				"group_url":  common.TelegramGroupURL,
			},
		})
		return
	}
	botName := strings.TrimPrefix(strings.TrimSpace(common.TelegramBotName), "@")
	botURL := "https://t.me/" + url.PathEscape(botName) + "?start=verify_" + url.QueryEscape(token)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"identified": false,
			"bot_url":    botURL,
			"group_url":  common.TelegramGroupURL,
			"expires_at": verification.TokenExpiresAt.UTC().Format(time.RFC3339),
		},
	})
}

func TelegramGroupVerificationStatus(c *gin.Context) {
	telegramGroupStatus(c)
}

func TelegramWebhook(c *gin.Context) {
	configuredSecret := strings.TrimSpace(common.TelegramWebhookSecret)
	providedSecret := strings.TrimSpace(c.GetHeader(telegramWebhookSecretHeader))
	if configuredSecret == "" || len(configuredSecret) != len(providedSecret) ||
		subtle.ConstantTimeCompare([]byte(configuredSecret), []byte(providedSecret)) != 1 {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false})
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	var update telegramWebhookUpdate
	if err := common.DecodeJson(c.Request.Body, &update); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false})
		return
	}
	token, ok := telegramVerificationToken(update)
	if !ok {
		c.JSON(http.StatusOK, gin.H{"success": true})
		return
	}

	telegramID := strconv.FormatInt(update.Message.From.ID, 10)
	_, replay, err := model.ConsumeTelegramVerification(token, telegramID, time.Now())
	if err != nil {
		message := "This verification link is invalid or expired. Please return to APIMaster and try again."
		if errors.Is(err, model.ErrTelegramAccountAlreadyLinked) {
			message = "This Telegram account has already been used by another APIMaster account."
		}
		_ = sendTelegramMessage(c, update.Message.Chat.ID, message, "Open APIMaster", "https://apimaster.ai/console/personal")
		c.JSON(http.StatusOK, gin.H{"success": true})
		return
	}
	if replay {
		c.JSON(http.StatusOK, gin.H{"success": true})
		return
	}
	if err := sendTelegramMessage(
		c,
		update.Message.Chat.ID,
		"APIMaster has identified your Telegram account. Join the community below, then return to APIMaster; the page will verify your membership automatically.",
		"Join APIMaster community",
		common.TelegramGroupURL,
	); err != nil {
		common.SysError("failed to send Telegram verification message: " + err.Error())
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func telegramVerificationToken(update telegramWebhookUpdate) (string, bool) {
	if update.Message == nil || update.Message.From == nil || update.Message.Chat.Type != "private" {
		return "", false
	}
	fields := strings.Fields(strings.TrimSpace(update.Message.Text))
	if len(fields) == 0 || fields[0] != "/start" && !strings.HasPrefix(fields[0], "/start@") {
		return "", false
	}
	if len(fields) != 2 || !strings.HasPrefix(fields[1], "verify_") {
		return "", false
	}
	token := strings.TrimPrefix(fields[1], "verify_")
	if len(token) != 43 {
		return "", false
	}
	return token, true
}

func sendTelegramMessage(c *gin.Context, chatID int64, text, buttonText, buttonURL string) error {
	payload, err := common.Marshal(telegramSendMessageRequest{
		ChatID: chatID,
		Text:   text,
		ReplyMarkup: telegramInlineKeyboard{InlineKeyboard: [][]telegramInlineButton{{{
			Text: buttonText,
			URL:  buttonURL,
		}}}},
	})
	if err != nil {
		return err
	}
	endpoint := telegramBotAPIURL("sendMessage")
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := telegramHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Telegram sendMessage returned HTTP %d", resp.StatusCode)
	}
	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := common.DecodeJson(resp.Body, &result); err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("Telegram sendMessage failed: %s", result.Description)
	}
	return nil
}

func telegramGroupStatus(c *gin.Context) {
	if !telegramGroupVerificationConfigured() {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "Telegram community verification is not configured",
		})
		return
	}

	userID := c.GetInt("id")
	verification, err := model.GetTelegramGroupVerificationByUserID(userID)
	if errors.Is(err, gorm.ErrRecordNotFound) || verification.TelegramID == nil || strings.TrimSpace(*verification.TelegramID) == "" {
		status := "not_started"
		if err == nil {
			if verification.TokenConsumedAt == nil && time.Now().Before(verification.TokenExpiresAt) {
				status = "waiting_for_bot"
			} else if verification.TokenConsumedAt == nil {
				status = "expired"
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Unable to load Telegram verification"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
			"configured": true,
			"identified": false,
			"joined":     false,
			"status":     status,
			"group_url":  common.TelegramGroupURL,
		}})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Unable to load Telegram verification"})
		return
	}

	status, joined, err := getTelegramGroupMembership(c.Request.Context(), *verification.TelegramID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := model.MarkTelegramGroupVerified(userID, joined, time.Now()); err != nil {
		common.SysError("failed to persist Telegram group verification status: " + err.Error())
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"configured": true,
		"identified": true,
		"joined":     joined,
		"status":     status,
		"group_url":  common.TelegramGroupURL,
		"checked_at": time.Now().UTC().Format(time.RFC3339),
	}})
}
