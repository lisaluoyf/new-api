package service

import (
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// Matches the production /opt/imgs cleanup policy. This describes cached
// images only; upstream media and videos have independent retention policies.
const cachedImageRetention = 12 * time.Hour

type logImageAvailability struct {
	Status         string `json:"status"`
	ExpiresAt      int64  `json:"expires_at,omitempty"`
	RetentionHours int    `json:"retention_hours"`
}

// EnrichLogsImageAvailability adds ephemeral preview metadata to authorized
// log responses. It never changes stored logs, generation status or billing,
// and only stats files in our own image cache (no upstream network requests).
func EnrichLogsImageAvailability(logs []*model.Log) {
	for _, log := range logs {
		if log == nil || log.Type != model.LogTypeConsume {
			continue
		}
		other, err := common.StrToMap(log.Other)
		if err != nil || other == nil {
			continue
		}
		urls := []string{}
		if result, ok := other["result_url"].(string); ok {
			urls = append(urls, result)
		}
		if results, ok := other["result_urls"].([]interface{}); ok {
			for _, result := range results {
				if result, ok := result.(string); ok {
					urls = append(urls, result)
				}
			}
		}
		availability := map[string]logImageAvailability{}
		now := time.Now()
		for _, result := range urls {
			result = strings.TrimSpace(result)
			if _, exists := availability[result]; exists {
				continue
			}
			if state, ok := cachedLogImageAvailability(result, now); ok {
				availability[result] = state
			}
		}
		// Do not trust stale metadata persisted by older callers.
		if _, hadAvailability := other["image_availability"]; len(availability) == 0 && !hadAvailability {
			continue
		}
		delete(other, "image_availability")
		if len(availability) > 0 {
			other["image_availability"] = availability
		}
		log.Other = common.MapToJsonStr(other)
	}
}

func cachedLogImageAvailability(rawURL string, now time.Time) (logImageAvailability, bool) {
	base, err := url.Parse(imageCachePublicBase)
	if err != nil {
		return logImageAvailability{}, false
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.User != nil || (u.Host != "" && (u.Host != base.Host || (u.Scheme != "http" && u.Scheme != "https"))) {
		return logImageAvailability{}, false
	}
	if u.Host == "" && u.Scheme != "" {
		return logImageAvailability{}, false
	}
	if path.Dir(u.Path)+"/" != base.Path {
		return logImageAvailability{}, false
	}
	name := path.Base(u.Path)
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return logImageAvailability{}, false
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif":
	default:
		return logImageAvailability{}, false
	}
	state := logImageAvailability{Status: "unavailable", RetentionHours: int(cachedImageRetention / time.Hour)}
	info, err := os.Stat(filepath.Join(imageCacheDir, name))
	if err == nil && info.Mode().IsRegular() && info.Size() > 0 {
		state.Status = "available"
		state.ExpiresAt = info.ModTime().Add(cachedImageRetention).Unix()
		return state, true
	}
	// Cache filenames encode output creation time, unlike request timestamps
	// which may precede completion by minutes or hours. An unknown name or a
	// filesystem error must never be called "expired" merely because of a 404.
	stamp := strings.TrimSuffix(name, path.Ext(name))
	nanos, parseErr := strconv.ParseInt(stamp, 10, 64)
	created := time.Unix(0, nanos)
	if parseErr == nil && created.Year() >= 2020 && !created.After(now) {
		state.ExpiresAt = created.Add(cachedImageRetention).Unix()
		if os.IsNotExist(err) && !now.Before(created.Add(cachedImageRetention)) {
			state.Status = "expired"
		}
	}
	return state, true
}
