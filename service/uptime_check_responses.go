package service

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// Responses-only routes need the same protocol for manual and scheduled pings
// as for real traffic. Metadata events alone do not prove usable model output.
func sendResponsesUptimeProbe(ctx context.Context, client *http.Client, baseURL, apiKey, modelName string, channel *model.Channel) (probeResult, *probeError) {
	prompt, maxTokens := uptimeProbePrompt, uint(64)
	if text, limit, ok := ChannelCodeReviewProbe(channel); ok {
		prompt, maxTokens = text, limit
	}
	body, err := common.Marshal(map[string]any{
		"model": modelName, "input": prompt, "max_output_tokens": maxTokens, "stream": true, "store": false,
	})
	if err != nil {
		return probeResult{}, &probeError{msg: fmt.Sprintf("marshal: %v", err)}
	}
	endpoint := strings.TrimSuffix(buildChatCompletionsURL(baseURL), "/chat/completions") + "/responses"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return probeResult{}, &probeError{msg: fmt.Sprintf("build request: %v", err)}
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "python-requests/2.31.0")
	start := time.Now()
	resp, err := client.Do(req)
	result := probeResult{LatencyMs: float64(time.Since(start).Milliseconds())}
	if err != nil {
		return result, &probeError{msg: fmt.Sprintf("network: %v", err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		var payload struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = common.Unmarshal(raw, &payload)
		kind := probeErrOther
		if strings.Contains(strings.ToLower(payload.Error.Code), "model_not_found") || strings.Contains(strings.ToLower(payload.Error.Message), "model not found") {
			kind = probeErrModel
		}
		detail := payload.Error.Message
		if detail == "" {
			detail = compactForNote(string(raw))
		}
		detail = strings.ReplaceAll(detail, apiKey, "[redacted]")
		if len(detail) > 500 {
			detail = detail[:500]
		}
		return result, &probeError{kind: kind, msg: fmt.Sprintf("Responses HTTP %d: %s", resp.StatusCode, detail)}
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, uptimeScannerBufferSize), uptimeScannerBufferSize)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var event struct {
			Type  string `json:"type"`
			Delta string `json:"delta"`
		}
		if common.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &event) != nil {
			continue
		}
		switch event.Type {
		case "error", "response.failed", "response.incomplete":
			return result, &probeError{msg: "Responses stream returned " + event.Type}
		case "response.output_text.delta":
			if strings.TrimSpace(event.Delta) != "" {
				return probeResult{LatencyMs: float64(time.Since(start).Milliseconds())}, nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return result, &probeError{msg: fmt.Sprintf("stream read: %v", err)}
	}
	return result, &probeError{msg: "Responses stream returned no output text"}
}
