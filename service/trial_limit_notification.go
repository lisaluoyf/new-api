package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/bytedance/gopkg/util/gopool"
)

var trialLimitNotificationHTTPClient = &http.Client{Timeout: 10 * time.Second}

// EnqueueGPTTrialLimitNotification is called only after a successful request
// settlement. It deliberately has no historical scan: launching this feature
// must not contact users who crossed the threshold before it existed.
func EnqueueGPTTrialLimitNotification(userID, subscriptionID int) {
	if userID <= 0 || subscriptionID <= 0 {
		return
	}
	gopool.Go(func() {
		notification, err := model.CreateTrialLimitNotificationIfReached(userID, subscriptionID)
		if err != nil {
			common.SysError(fmt.Sprintf("trial-limit notification enqueue failed user_id=%d subscription_id=%d: %v", userID, subscriptionID, err))
			return
		}
		if notification == nil {
			return
		}
		deliverGPTTrialLimitNotification(notification)
	})
}

func deliverGPTTrialLimitNotification(notification *model.TrialLimitNotification) {
	if notification == nil {
		return
	}
	user, err := model.GetUserById(notification.UserId, false)
	if err != nil {
		_ = model.MarkTrialLimitNotificationFailed(notification.Id, err)
		return
	}
	variant := model.TrialLimitNotificationStandard
	if eligible, _ := model.IsFirstTopupPromoEligible(user.Id); eligible {
		variant = model.TrialLimitNotificationFirstTopupPromo
	}
	text, button := renderGPTTrialLimitNotification(variant)
	url := trialLimitNotificationRedirectURL(notification.ClickToken)

	telegramID, telegramOK := parseNumericSocialID(user.TelegramId)
	discordID, discordOK := parseNumericSocialID(user.DiscordId)
	var deliveryErrors []string
	if telegramOK {
		if err := sendTrialLimitTelegramMessage(telegramID, text, button, url); err == nil {
			_ = model.MarkTrialLimitNotificationSent(notification.Id, model.TrialLimitNotificationTelegram, variant, "")
			return
		} else {
			deliveryErrors = append(deliveryErrors, "telegram: "+err.Error())
		}
	}
	if discordOK {
		if err := sendTrialLimitDiscordMessage(discordID, text, button, url); err == nil {
			_ = model.MarkTrialLimitNotificationSent(notification.Id, model.TrialLimitNotificationDiscord, variant, "")
			return
		} else {
			deliveryErrors = append(deliveryErrors, "discord: "+err.Error())
		}
	}
	if !telegramOK && !discordOK {
		_ = model.MarkTrialLimitNotificationUnavailable(notification.Id, nil)
		return
	}
	_ = model.MarkTrialLimitNotificationFailed(notification.Id, errors.New(strings.Join(deliveryErrors, "; ")))
}

func parseNumericSocialID(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed == 0 {
		return "", false
	}
	return value, true
}

func trialLimitNotificationRedirectURL(token string) string {
	base := strings.TrimRight(strings.TrimSpace(system_setting.ServerAddress), "/")
	if base == "" || strings.HasPrefix(base, "http://localhost") {
		base = "https://apimaster.ai"
	}
	return base + "/api/trial-limit/redirect/" + token
}

func renderGPTTrialLimitNotification(variant string) (string, string) {
	text := "⚠️ GPT 体验额度即将用尽\n\n" +
		"💳 继续调用需要充值钱包余额。\n\n" +
		"🔑 充值后，无需重新配置，当前 API Key 即可继续使用。\n\n" +
		"💰 体验额度按官方价格计费；钱包余额按折扣价计费，例如 GPT-5.6 可节省 97%。\n\n" +
		"🤖 一个 API Key 即可使用 GPT、Claude、Gemini、Kimi、GLM、DeepSeek 等主流模型。"
	if variant != model.TrialLimitNotificationFirstTopupPromo {
		return text, "🚀 立即充值并继续使用"
	}
	discountPercent := int(common.FirstTopupPromoDiscount*100 + 0.5)
	payAmount := float64(common.FirstTopupPromoAmount) * common.FirstTopupPromoDiscount
	text += fmt.Sprintf("\n\n🎁 首充限时 %d 折：充值 $%d，仅需支付 $%.2f。", discountPercent, common.FirstTopupPromoAmount, payAmount)
	return text, fmt.Sprintf("🚀 立即充值，享 %d 折", discountPercent)
}

func sendTrialLimitTelegramMessage(chatID, text, button, targetURL string) error {
	if strings.TrimSpace(common.TelegramBotToken) == "" {
		return errors.New("Telegram bot is not configured")
	}
	payload, err := common.Marshal(map[string]any{
		"chat_id":      chatID,
		"text":         text,
		"reply_markup": map[string]any{"inline_keyboard": [][]map[string]string{{{"text": button, "url": targetURL}}}},
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	endpoint := "https://api.telegram.org/bot" + common.TelegramBotToken + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := trialLimitNotificationHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
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

func sendTrialLimitDiscordMessage(recipientID, text, button, targetURL string) error {
	botToken := strings.TrimSpace(os.Getenv("DISCORD_COMMUNITY_BOT_TOKEN"))
	if botToken == "" {
		return errors.New("Discord bot is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	createPayload, err := common.Marshal(map[string]string{"recipient_id": recipientID})
	if err != nil {
		return err
	}
	channelID, err := doDiscordTrialLimitRequest(ctx, botToken, http.MethodPost, "https://discord.com/api/v10/users/@me/channels", createPayload, true)
	if err != nil {
		return err
	}
	messagePayload, err := common.Marshal(map[string]any{
		"content": text,
		"components": []map[string]any{{
			"type":       1,
			"components": []map[string]any{{"type": 2, "style": 5, "label": button, "url": targetURL}},
		}},
	})
	if err != nil {
		return err
	}
	_, err = doDiscordTrialLimitRequest(ctx, botToken, http.MethodPost, "https://discord.com/api/v10/channels/"+channelID+"/messages", messagePayload, false)
	return err
}

func doDiscordTrialLimitRequest(ctx context.Context, botToken, method, endpoint string, payload []byte, needsChannelID bool) (string, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bot "+botToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := trialLimitNotificationHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("Discord API returned HTTP %d", resp.StatusCode)
	}
	if !needsChannelID {
		return "", nil
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := common.DecodeJson(resp.Body, &result); err != nil {
		return "", err
	}
	if _, ok := parseNumericSocialID(result.ID); !ok {
		return "", errors.New("Discord returned an invalid DM channel")
	}
	return result.ID, nil
}
