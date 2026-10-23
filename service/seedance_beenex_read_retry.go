package service

import (
	"context"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/model"
)

// Only a just-created, confirmed material ID may need a short read-after-write
// delay. Never retry creates, missing IDs, old/deleted materials or other errors.
func beeNexRetryNewAssetRead(ctx context.Context, resource *model.SeedanceResource, action string, fields map[string]any, call func() (map[string]any, error)) (map[string]any, error) {
	for attempt := 0; ; attempt++ {
		envelope, err := call()
		if attempt >= 3 || !beeNexNewAssetReadPending(resource, action, fields, err) {
			return envelope, err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func beeNexNewAssetReadPending(resource *model.SeedanceResource, action string, fields map[string]any, err error) bool {
	if resource == nil || resource.ChannelID != 274 || resource.Kind != "asset" || resource.Status != "Processing" || resource.UpstreamID == "" || action != "GetAsset" || seedanceString(fields, "Id") != resource.UpstreamID || resource.CreatedAt <= 0 || resource.DeletedAt.Valid {
		return false
	}
	age := time.Since(time.Unix(resource.CreatedAt, 0))
	if age < 0 || age > 90*time.Second {
		return false
	}
	var failure *SeedanceAPIError
	if !errors.As(err, &failure) || failure.UpstreamError == nil || failure.UpstreamError.StatusCode != 200 {
		return false
	}
	body, _ := failure.UpstreamError.Body.(map[string]any)
	result, _ := body["Result"].(map[string]any)
	detail, _ := result["Error"].(map[string]any)
	return seedanceString(result, "Id") == "" && seedanceString(detail, "Code") == "InvalidAsset"
}
