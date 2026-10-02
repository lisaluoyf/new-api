package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

func sendEmbeddingUptimeProbe(ctx context.Context, client *http.Client, baseURL, apiKey, modelName string) (probeResult, *probeError) {
	endpoint := strings.TrimSuffix(buildChatCompletionsURL(baseURL), "chat/completions") + "embeddings"
	body, err := common.Marshal(map[string]any{
		"model": modelName, "input": uptimeProbePrompt, "encoding_format": "float",
	})
	if err != nil {
		return probeResult{}, &probeError{msg: err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return probeResult{}, &probeError{msg: err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	start := time.Now()
	resp, err := client.Do(req)
	result := probeResult{LatencyMs: float64(time.Since(start).Microseconds()) / 1000}
	if err != nil {
		return result, &probeError{msg: "embedding request failed"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return result, &probeError{msg: fmt.Sprintf("embedding HTTP %d", resp.StatusCode)}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	result.LatencyMs = float64(time.Since(start).Microseconds()) / 1000
	if err != nil {
		return result, &probeError{msg: "embedding response read failed"}
	}
	var response struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if common.Unmarshal(data, &response) != nil || len(response.Data) != 1 || len(response.Data[0].Embedding) == 0 {
		return result, &probeError{msg: "invalid embedding response"}
	}
	nonzero := false
	for _, value := range response.Data[0].Embedding {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return result, &probeError{msg: "invalid embedding vector"}
		}
		nonzero = nonzero || value != 0
	}
	if !nonzero {
		return result, &probeError{msg: "empty embedding vector"}
	}
	return result, nil
}
