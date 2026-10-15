package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/tidwall/gjson"
	"gorm.io/gorm/clause"
)

var ErrVideoFeeWebhookAuth = errors.New("invalid VideoFee callback signature")
var ErrVideoFeeWebhookPayload = errors.New("invalid VideoFee callback payload")

type videoFeeWebhookPayload struct {
	EventID   string `json:"event_id"`
	EventType string `json:"event_type"`
	Data      struct {
		TaskID string `json:"task_id"`
		Status string `json:"status"`
	} `json:"data"`
}

// Only channel 273 opts into automatic supplier callbacks; explicit legacy
// caller callbacks are preserved. Polling stays enabled regardless.
func VideoFeeWebhookURL(channelID int) string {
	if channelID != constant.VideoFeeSeedanceChannelID || os.Getenv("VIDEOFEE_WEBHOOK_ENABLED") == "false" {
		return ""
	}
	base := MediaTaskWebhookBase()
	if base == "" {
		return ""
	}
	return base + "/videofee/callback"
}

func VerifyVideoFeeWebhook(secret string, headers http.Header, raw []byte, now int64) bool {
	stamp := headers.Get("X-Webhook-Timestamp")
	ts, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil || ts < now-300 || ts > now+300 || headers.Get("X-Webhook-Key-Id") != "v1" {
		return false
	}
	id := headers.Get("X-Webhook-Event-Id")
	if id == "" || len(id) > 128 || secret == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(stamp + "." + id + "."))
	mac.Write(raw)
	expected := "v1=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(headers.Get("X-Webhook-Signature")))
}

// Secrets are fetched with the task's supplier credential, never an APIMaster
// customer key. A short in-memory cache avoids a network call for every event.
var videoFeeKeyCache = struct {
	sync.Mutex
	keys map[string]struct {
		secret  string
		expires int64
	}
}{keys: make(map[string]struct {
	secret  string
	expires int64
})}

func videoFeeSigningKey(ctx context.Context, ch *model.Channel, key string, refresh bool) (string, error) {
	hash := sha256.Sum256([]byte(ch.GetBaseURL() + "\x00" + key))
	cacheID := hex.EncodeToString(hash[:])
	now := time.Now().Unix()
	videoFeeKeyCache.Lock()
	cached := videoFeeKeyCache.keys[cacheID]
	videoFeeKeyCache.Unlock()
	if !refresh && cached.expires > now {
		return cached.secret, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(ch.GetBaseURL(), "/")+"/v1/webhooks/signing-key", nil)
	if err != nil {
		return "", fmt.Errorf("VideoFee signing key request failed")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	client, err := GetHttpClientWithProxy(ch.GetSetting().Proxy)
	if err != nil || client == nil {
		return "", fmt.Errorf("VideoFee signing key client failed")
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := copyClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("VideoFee signing key unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("VideoFee signing key HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", fmt.Errorf("VideoFee signing key read failed")
	}
	var result struct {
		KeyID      string `json:"key_id"`
		Algorithm  string `json:"algorithm"`
		SigningKey string `json:"signing_key"`
	}
	if common.Unmarshal(body, &result) != nil || result.KeyID != "v1" || result.Algorithm != "HMAC-SHA256" || result.SigningKey == "" {
		return "", fmt.Errorf("VideoFee signing key response invalid")
	}
	videoFeeKeyCache.Lock()
	videoFeeKeyCache.keys[cacheID] = struct {
		secret  string
		expires int64
	}{result.SigningKey, now + 60}
	videoFeeKeyCache.Unlock()
	return result.SigningKey, nil
}

func ReceiveVideoFeeWebhook(ctx context.Context, headers http.Header, raw []byte) error {
	var event videoFeeWebhookPayload
	if common.Unmarshal(raw, &event) != nil || event.EventID == "" || len(event.EventID) > 128 || event.EventID != headers.Get("X-Webhook-Event-Id") || event.Data.TaskID == "" {
		return ErrVideoFeeWebhookPayload
	}
	switch event.EventType {
	case "video.task.completed":
		if event.Data.Status != "SUCCESS" {
			return ErrVideoFeeWebhookPayload
		}
	case "video.task.failed":
		if event.Data.Status != "FAILURE" && event.Data.Status != "CANCELLED" {
			return ErrVideoFeeWebhookPayload
		}
	default:
		return ErrVideoFeeWebhookPayload
	}
	task, exists, err := model.GetByTaskOrUpstreamTaskId(event.Data.TaskID)
	if err != nil || !exists || task == nil {
		return fmt.Errorf("VideoFee callback task not available")
	}
	if task.ChannelId != constant.VideoFeeSeedanceChannelID || task.Platform != constant.TaskPlatformVideoFee || task.GetUpstreamTaskID() != event.Data.TaskID {
		return ErrVideoFeeWebhookPayload
	}
	ch, err := model.GetChannelById(task.ChannelId, true)
	if err != nil {
		return fmt.Errorf("VideoFee callback channel unavailable")
	}
	key := ch.Key
	if task.PrivateData.Key != "" {
		key = task.PrivateData.Key
	}
	secret, err := videoFeeSigningKey(ctx, ch, key, false)
	if err != nil {
		return err
	}
	if !VerifyVideoFeeWebhook(secret, headers, raw, time.Now().Unix()) {
		// The supplier has no dual-key rotation window: refresh on signature mismatch.
		secret, err = videoFeeSigningKey(ctx, ch, key, true)
		if err != nil {
			return err
		}
		if !VerifyVideoFeeWebhook(secret, headers, raw, time.Now().Unix()) {
			return ErrVideoFeeWebhookAuth
		}
	}
	now := time.Now().Unix()
	inbox := model.VideoFeeWebhookInbox{ID: event.EventID, TaskID: task.ID, Payload: string(redactVideoResponseBody(raw)), Status: "pending", ReceivedAt: now, NextAt: now}
	if err := model.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&inbox).Error; err != nil {
		return err
	}
	var saved model.VideoFeeWebhookInbox
	if err := model.DB.WithContext(ctx).First(&saved, "id = ?", event.EventID).Error; err != nil {
		return err
	}
	if saved.TaskID != task.ID || saved.Payload != inbox.Payload {
		return ErrVideoFeeWebhookPayload
	}
	return nil
}

// Feed the verified supplier query-shaped payload into the exact polling path,
// preserving result/last-frame parsing, billing, terminal CAS, notifications and SD.
type videoFeeCallbackAdaptor struct {
	TaskPollingAdaptor
	payload []byte
}

func (a videoFeeCallbackAdaptor) FetchTask(string, string, map[string]any, string) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(a.payload))}, nil
}

func processVideoFeeWebhook(ctx context.Context, event *model.VideoFeeWebhookInbox) error {
	var task model.Task
	if err := model.DB.WithContext(ctx).First(&task, event.TaskID).Error; err != nil {
		return err
	}
	if task.ChannelId != constant.VideoFeeSeedanceChannelID || task.Platform != constant.TaskPlatformVideoFee {
		return ErrVideoFeeWebhookPayload
	}
	if task.Status == model.TaskStatusSuccess {
		return nil
	}
	if task.Status == model.TaskStatusFailure {
		return enrichVideoFeeTerminalFailure(ctx, &task, event.Payload)
	}
	if GetTaskAdaptorFunc == nil {
		return fmt.Errorf("VideoFee adaptor not ready")
	}
	adaptor := GetTaskAdaptorFunc(task.Platform)
	if adaptor == nil {
		return fmt.Errorf("VideoFee adaptor unavailable")
	}
	ch, err := model.GetChannelById(task.ChannelId, true)
	if err != nil {
		return err
	}
	adaptor.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: ch.GetBaseURL(), ApiKey: ch.Key}})
	err = updateVideoSingleTask(ctx, videoFeeCallbackAdaptor{adaptor, []byte(event.Payload)}, ch, task.GetUpstreamTaskID(), map[string]*model.Task{task.GetUpstreamTaskID(): &task})
	if err != nil {
		return err
	}
	var fresh model.Task
	if err = model.DB.WithContext(ctx).First(&fresh, task.ID).Error; err != nil {
		return err
	}
	if fresh.Status == model.TaskStatusRetrying {
		return nil
	}
	if fresh.Status != model.TaskStatusSuccess && fresh.Status != model.TaskStatusFailure {
		return fmt.Errorf("VideoFee terminal update not persisted")
	}
	// Polling may have won the terminal CAS after this callback loaded the task.
	// Enrich only generic text in the winning row; do not rerun settlement.
	if fresh.Status == model.TaskStatusFailure {
		return enrichVideoFeeTerminalFailure(ctx, &fresh, event.Payload)
	}
	return nil
}

// The payload was authenticated and durably bound to this task at receipt.
// Never use a contradictory event or overwrite an existing detailed reason.
func enrichVideoFeeTerminalFailure(ctx context.Context, task *model.Task, raw string) error {
	if !gjson.Valid(raw) {
		return ErrVideoFeeWebhookPayload
	}
	data := gjson.Get(raw, "data")
	if data.Get("task_id").String() != task.GetUpstreamTaskID() {
		return ErrVideoFeeWebhookPayload
	}
	switch strings.ToUpper(data.Get("status").String()) {
	case "FAILURE", "FAILED", "CANCELLED", "CANCELED":
	default:
		return nil
	}
	value := data.Get("fail_reason")
	if value.Type != gjson.String || strings.TrimSpace(value.String()) == "" {
		value = data.Get("error.message")
	}
	if value.Type != gjson.String {
		return nil
	}
	reason := strings.TrimSpace(value.String())
	candidate := *task
	candidate.FailReason = reason
	publicReason := PublicTaskFailure(&candidate)
	if model.IsGenericTaskFailureReason(publicReason) || publicReason == "[redacted]" {
		return nil
	}
	_, err := model.EnrichVideoFeeFailureReason(ctx, task, reason)
	return err
}

func RunVideoFeeWebhookBatch() error {
	now := time.Now().Unix()
	var events []model.VideoFeeWebhookInbox
	if err := model.DB.Where("status = ? AND next_at <= ? AND lease_until < ?", "pending", now, now).Order("received_at").Limit(32).Find(&events).Error; err != nil {
		return err
	}
	for _, event := range events {
		now = time.Now().Unix()
		claim := model.DB.Model(&model.VideoFeeWebhookInbox{}).Where("id = ? AND status = ? AND lease_until < ?", event.ID, "pending", now).Updates(map[string]any{"lease_until": now + 120, "attempts": event.Attempts + 1})
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected != 1 {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		err := processVideoFeeWebhook(ctx, &event)
		cancel()
		updates := map[string]any{"lease_until": 0, "next_at": time.Now().Unix() + 10}
		if err == nil {
			updates["status"] = "processed"
			updates["processed_at"] = time.Now().Unix()
		}
		if err := model.DB.Model(&model.VideoFeeWebhookInbox{}).Where("id = ? AND lease_until = ?", event.ID, now+120).Updates(updates).Error; err != nil {
			return err
		}
	}
	return nil
}

var videoFeeWorkerOnce sync.Once

func StartVideoFeeWebhookWorker() {
	if !common.IsMasterNode {
		return
	}
	videoFeeWorkerOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			lastCleanup := int64(0)
			for range ticker.C {
				if err := RunVideoFeeWebhookBatch(); err != nil {
					common.SysError("VideoFee callback inbox processing failed")
				}
				now := time.Now().Unix()
				if now-lastCleanup >= 3600 {
					// Bounded deletion, portable across SQLite/MySQL/PostgreSQL.
					var old []model.VideoFeeWebhookInbox
					if model.DB.Select("id").Where("status = ? AND processed_at < ?", "processed", now-8*86400).Limit(100).Find(&old).Error == nil {
						ids := make([]string, 0, len(old))
						for _, e := range old {
							ids = append(ids, e.ID)
						}
						if len(ids) > 0 {
							model.DB.Where("id IN ?", ids).Delete(&model.VideoFeeWebhookInbox{})
						}
					}
					lastCleanup = now
				}
			}
		}()
	})
}
