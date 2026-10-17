package service

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestCachedLogImageAvailabilityUsesActualFileAndRetention(t *testing.T) {
	oldDir := imageCacheDir
	imageCacheDir = t.TempDir()
	t.Cleanup(func() { imageCacheDir = oldDir })
	now := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	oldName := fmt.Sprintf("%d.png", now.Add(-70*time.Hour).UnixNano())
	recentName := fmt.Sprintf("%d.png", now.Add(-time.Hour).UnixNano())
	for _, tc := range []struct {
		name, url, want string
		accepted        bool
	}{
		{"expired complaint", "https://apimaster.ai/imgs/" + oldName, "expired", true},
		{"missing within retention", "/imgs/" + recentName, "unavailable", true},
		{"unknown old filename", "/imgs/legacy.png", "unavailable", true},
		{"external URL", "https://upstream.example/imgs/" + oldName, "", false},
		{"unrelated local path", "/uploads/" + oldName, "", false},
		{"path traversal", "/imgs/../" + oldName, "", false},
		{"encoded traversal", "/imgs/%2e%2e%2f" + oldName, "", false},
		{"data URL", "data:image/png;base64,aGVsbG8=", "", false},
		{"nested path", "/imgs/nested/" + oldName, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, accepted := cachedLogImageAvailability(tc.url, now)
			require.Equal(t, tc.accepted, accepted)
			if accepted {
				require.Equal(t, tc.want, state.Status)
				require.Equal(t, 24, state.RetentionHours)
			}
		})
	}
	// Hourly pruning may leave an image available beyond its nominal 24h TTL.
	// Actual file existence wins over elapsed time.
	file := filepath.Join(imageCacheDir, oldName)
	require.NoError(t, os.WriteFile(file, []byte("image"), 0600))
	modified := now.Add(-24*time.Hour - 20*time.Minute)
	require.NoError(t, os.Chtimes(file, modified, modified))
	state, accepted := cachedLogImageAvailability("/imgs/"+oldName, now)
	require.True(t, accepted)
	require.Equal(t, "available", state.Status)
	require.Equal(t, modified.Add(24*time.Hour).Unix(), state.ExpiresAt)
}

func TestEnrichImageAvailabilityKeepsGenerationAndStoredLogUnchanged(t *testing.T) {
	oldDir := imageCacheDir
	imageCacheDir = t.TempDir()
	t.Cleanup(func() { imageCacheDir = oldDir })
	url := "https://apimaster.ai/imgs/1791944252211514321.png"
	original := common.MapToJsonStr(map[string]interface{}{
		"result_url":   url,
		"result_urls":  []interface{}{url, "https://upstream.example/image.png"},
		"request_data": map[string]interface{}{"n": 1},
	})
	log := &model.Log{Id: 4641398, Type: model.LogTypeConsume, Quota: 100,
		Content: "generation succeeded", Other: original}
	failed := &model.Log{Type: model.LogTypeError, Other: original}
	EnrichLogsImageAvailability([]*model.Log{nil, log, failed})
	require.Equal(t, model.LogTypeConsume, log.Type)
	require.Equal(t, 100, log.Quota)
	require.Equal(t, "generation succeeded", log.Content)
	require.Equal(t, original, failed.Other)
	other, err := common.StrToMap(log.Other)
	require.NoError(t, err)
	require.Equal(t, url, other["result_url"])
	states := other["image_availability"].(map[string]interface{})
	require.Len(t, states, 1)
	require.Equal(t, "expired", states[url].(map[string]interface{})["status"])
	// Every read recomputes availability; stale persisted status cannot override
	// the presence of a restored file.
	require.NoError(t, os.WriteFile(filepath.Join(imageCacheDir, "1791944252211514321.png"), []byte("image"), 0600))
	EnrichLogsImageAvailability([]*model.Log{log})
	other, err = common.StrToMap(log.Other)
	require.NoError(t, err)
	states = other["image_availability"].(map[string]interface{})
	require.Equal(t, "available", states[url].(map[string]interface{})["status"])
}
