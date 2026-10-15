package service

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
)

type videoFailureRefreshEntry struct {
	ready   chan struct{}
	expires time.Time
	error   *dto.OpenAIVideoError
}

var videoFailureRefreshCache = struct {
	sync.Mutex
	entries map[string]*videoFailureRefreshEntry
}{entries: make(map[string]*videoFailureRefreshEntry)}

// RefreshVideoFeeFailureDetails enriches an ownership-checked task in memory.
// It never changes terminal state, stored task evidence, billing or webhooks.
// Empty failures may gain a useful reason later at the provider. Coalesce and
// cache both successful/empty lookups, with a bounded cache and request timeout.
func RefreshVideoFeeFailureDetails(ctx context.Context, task *model.Task) {
	if task == nil || task.Status != model.TaskStatusFailure || task.Platform != constant.TaskPlatformVideoFee ||
		model.NormalizeVerifiedVideoModel(task.Properties.OriginModelName) == "" ||
		(task.PrivateData.SeedanceFallback != nil && task.PrivateData.SeedanceFallback.Started) {
		return
	}
	if ctx.Err() != nil {
		return
	}
	current := PublicTaskError(task)
	if current == nil || !model.IsGenericTaskFailureReason(current.Message) {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	now := time.Now()
	videoFailureRefreshCache.Lock()
	entry := videoFailureRefreshCache.entries[task.TaskID]
	if entry == nil || now.After(entry.expires) {
		for id, cached := range videoFailureRefreshCache.entries {
			if now.After(cached.expires) {
				delete(videoFailureRefreshCache.entries, id)
			}
		}
		if len(videoFailureRefreshCache.entries) >= 256 {
			videoFailureRefreshCache.Unlock()
			return
		}
		entry = &videoFailureRefreshEntry{ready: make(chan struct{}), expires: now.Add(5 * time.Minute)}
		videoFailureRefreshCache.entries[task.TaskID] = entry
		videoFailureRefreshCache.Unlock()
		entry.error = fetchVideoFeeFailureDetails(ctx, task)
		close(entry.ready)
	} else {
		videoFailureRefreshCache.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-entry.ready:
		}
	}
	if entry.error == nil {
		return
	}
	var data map[string]any
	if common.Unmarshal(task.Data, &data) != nil || data == nil {
		data = map[string]any{}
	}
	// A normalized error takes precedence over the saved generic wrapper, while
	// retaining other result metadata on this request's private in-memory copy.
	data["error"] = entry.error
	if raw, err := common.Marshal(data); err == nil {
		task.Data = raw
		task.FailReason = entry.error.Message
	}
}

func fetchVideoFeeFailureDetails(ctx context.Context, task *model.Task) *dto.OpenAIVideoError {
	channel, err := model.GetChannelById(task.ChannelId, true)
	if err != nil || channel == nil || task.GetUpstreamTaskID() == "" {
		return nil
	}
	base := strings.TrimRight(channel.GetBaseURL(), "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/video/generations/"+url.PathEscape(task.GetUpstreamTaskID()), nil)
	if err != nil {
		return nil
	}
	key := task.PrivateData.Key
	if key == "" {
		key = channel.Key
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	client, err := GetHttpClientWithProxy(channel.GetSetting().Proxy)
	if err != nil || client == nil {
		return nil
	}
	response, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil
	}
	var identity struct {
		Data struct {
			TaskID string `json:"task_id"`
		} `json:"data"`
	}
	if common.Unmarshal(raw, &identity) != nil || identity.Data.TaskID != task.GetUpstreamTaskID() {
		return nil
	}
	code, message := model.VideoFeeFailureDetails(raw)
	if code == "" || message == "" {
		return nil
	}
	candidate := *task
	candidate.Data = raw
	candidate.FailReason = message
	public := PublicTaskError(&candidate)
	if public == nil || model.IsGenericTaskFailureReason(public.Message) {
		return nil
	}
	return public
}
