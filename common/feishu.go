package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	feishuTokenMu     sync.Mutex
	feishuCachedToken string
	feishuTokenExpiry time.Time
)

func FeishuAppID() string     { return os.Getenv("FEISHU_APP_ID") }
func FeishuAppSecret() string { return os.Getenv("FEISHU_APP_SECRET") }
func FeishuOpsChatID() string { return os.Getenv("FEISHU_OPS_CHAT_ID") }
func FeishuRiskChatID() string {
	if chatID := os.Getenv("FEISHU_RISK_CHAT_ID"); chatID != "" {
		return chatID
	}
	return "oc_76aa27559f749b7b23a00b8694b7c259"
}
func FeishuChannelChatID() string {
	chatID := os.Getenv("FEISHU_CHANNEL_CHAT_ID")
	if chatID != "" {
		return chatID
	}
	return FeishuOpsChatID()
}
func FeishuNewAPILogChatID() string {
	return os.Getenv("FEISHU_NEWAPI_LOG_CHAT_ID")
}

func getFeishuToken() (string, error) {
	feishuTokenMu.Lock()
	defer feishuTokenMu.Unlock()
	if time.Now().Before(feishuTokenExpiry) {
		return feishuCachedToken, nil
	}
	body, _ := json.Marshal(map[string]string{
		"app_id":     FeishuAppID(),
		"app_secret": FeishuAppSecret(),
	})
	resp, err := http.Post(
		"https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var result struct {
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	feishuCachedToken = result.TenantAccessToken
	feishuTokenExpiry = time.Now().Add(time.Duration(result.Expire-60) * time.Second)
	return feishuCachedToken, nil
}

// SendFeishuCard sends a markdown card message to the given Feishu chat_id.
// title is bold, lines are appended as body. Silently skips if env vars are missing.
func SendFeishuCard(chatID, title string, lines []string) error {
	if FeishuAppID() == "" || FeishuAppSecret() == "" || chatID == "" {
		return nil
	}
	token, err := getFeishuToken()
	if err != nil {
		return err
	}

	mdContent := "**" + title + "**\n" + strings.Join(lines, "\n")
	card := map[string]any{
		"schema": "2.0",
		"body": map[string]any{
			"elements": []any{
				map[string]any{"tag": "markdown", "content": mdContent},
			},
		},
	}
	cardJSON, _ := json.Marshal(card)

	payload := map[string]any{
		"receive_id": chatID,
		"msg_type":   "interactive",
		"content":    string(cardJSON),
	}
	payloadJSON, _ := json.Marshal(payload)

	req, err := http.NewRequest(
		"POST",
		"https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type=chat_id",
		bytes.NewReader(payloadJSON),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices || result.Code != 0 {
		return fmt.Errorf("Feishu API error: status=%d code=%d msg=%s", resp.StatusCode, result.Code, result.Msg)
	}
	return nil
}
