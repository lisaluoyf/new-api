package service

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// The release script injects only the SSH alert webhook, without introducing
// a required file mount or importing unrelated SSH-monitor settings.
func callCostAlertWebhook() (string, error) {
	webhook := strings.TrimSpace(os.Getenv("CALL_COST_ALERT_WEBHOOK_URL"))
	parsed, err := url.Parse(webhook)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "open.feishu.cn" ||
		parsed.User != nil || !strings.HasPrefix(parsed.Path, "/open-apis/bot/v2/hook/") ||
		strings.TrimPrefix(parsed.Path, "/open-apis/bot/v2/hook/") == "" {
		return "", fmt.Errorf("SSH alert webhook is missing or invalid")
	}
	return webhook, nil
}

// Dedicated timeout/response validation; do not alter the existing SSH or app
// notification helpers. Webhook credentials must never appear in error logs.
func sendCallCostAlert(webhook string, card map[string]any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return deliverCallCostAlert(ctx, &http.Client{Timeout: 10 * time.Second}, webhook, card)
}

func deliverCallCostAlert(ctx context.Context, client *http.Client, webhook string, card map[string]any) error {
	payload, err := common.Marshal(map[string]any{"msg_type": "interactive", "card": card})
	if err != nil {
		return fmt.Errorf("call-cost alert: cannot encode Feishu card")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("call-cost alert: invalid Feishu webhook request")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("call-cost alert: Feishu webhook request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("call-cost alert: Feishu HTTP status=%d", resp.StatusCode)
	}
	var result struct {
		Code       *int `json:"code"`
		StatusCode *int `json:"StatusCode"`
	}
	if err := common.DecodeJson(resp.Body, &result); err != nil {
		return fmt.Errorf("call-cost alert: invalid Feishu webhook response")
	}
	if result.Code == nil && result.StatusCode == nil {
		return fmt.Errorf("call-cost alert: missing Feishu response code")
	}
	if result.Code != nil && *result.Code != 0 || result.StatusCode != nil && *result.StatusCode != 0 {
		return fmt.Errorf("call-cost alert: Feishu webhook rejected message")
	}
	return nil
}
