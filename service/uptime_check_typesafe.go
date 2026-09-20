package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"io"
	"net/http"
	"strings"
	"time"
)

func sendTypeSafeUptimeProbe(ctx context.Context, client *http.Client, baseURL, apiKey, modelName string) (probeResult, *probeError) {
	payload := dto.TypeSafeRequest{Model: modelName, State: json.RawMessage(`"The service is working."`), Questions: map[string]json.RawMessage{"healthy": json.RawMessage(`{"type":"noul","instructions":"Does the text say the service is working?"}`)}}
	body, err := common.Marshal(payload)
	if err != nil {
		return probeResult{}, &probeError{msg: err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/systemone", bytes.NewReader(body))
	if err != nil {
		return probeResult{}, &probeError{msg: err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	start := time.Now()
	resp, err := client.Do(req)
	result := probeResult{LatencyMs: float64(time.Since(start).Microseconds()) / 1000}
	if err != nil {
		return result, &probeError{msg: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return result, &probeError{msg: fmt.Sprintf("TypeSafe HTTP %d", resp.StatusCode)}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return result, &probeError{msg: err.Error()}
	}
	if _, err = dto.ParseTypeSafeResponse(data, &payload); err != nil {
		return result, &probeError{msg: err.Error()}
	}
	return result, nil
}
