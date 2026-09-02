package controller

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const miaInternalServiceKeyHeader = "X-Mia-Internal-Key"

var miaTelegramHTTPClient = &http.Client{Timeout: 3 * time.Second}

func miaTelegramForwardingConfigured() bool {
	return strings.TrimSpace(common.MiaTelegramWebhookURL) != "" &&
		strings.TrimSpace(common.MiaInternalServiceKey) != ""
}

func forwardTelegramUpdateToMia(ctx context.Context, payload []byte) error {
	if !miaTelegramForwardingConfigured() {
		return nil
	}

	endpoint := strings.TrimSpace(common.MiaTelegramWebhookURL)
	parsedURL, err := url.Parse(endpoint)
	if err != nil || parsedURL.Host == "" || parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return errors.New("invalid Mia Telegram webhook URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return errors.New("unable to prepare Mia Telegram update")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(miaInternalServiceKeyHeader, strings.TrimSpace(common.MiaInternalServiceKey))

	resp, err := miaTelegramHTTPClient.Do(req)
	if err != nil {
		return errors.New("unable to reach Mia Telegram webhook")
	}
	defer resp.Body.Close()
	_, _ = io.CopyN(io.Discard, resp.Body, 4096)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return errors.New("Mia Telegram webhook rejected update")
	}
	return nil
}
