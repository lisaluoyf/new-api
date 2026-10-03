package service

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var ErrTaskWebhookAddressBlocked = errors.New("webhook address is not public")

const TaskWebhookContextKey = "apimaster_task_webhook"

func TaskWebhookEncryptionKey() ([]byte, error) {
	secret := os.Getenv("TASK_WEBHOOK_MASTER_KEY")
	if secret == "" {
		secret = os.Getenv("CRYPTO_SECRET")
	}
	if secret == "" {
		secret = os.Getenv("SESSION_SECRET")
	}
	if len(secret) < 32 {
		return nil, errors.New("stable webhook encryption key is not configured")
	}
	sum := sha256.Sum256([]byte("apimaster-task-webhooks:v1:" + secret))
	return sum[:], nil
}
func EncryptTaskWebhookSecret(secret string) (string, error) {
	key, err := TaskWebhookEncryptionKey()
	if err != nil {
		return "", err
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := g.Seal(nonce, nonce, []byte(secret), []byte("task-webhook-secret-v1"))
	return base64.RawStdEncoding.EncodeToString(sealed), nil
}
func DecryptTaskWebhookSecret(value string) (string, error) {
	key, err := TaskWebhookEncryptionKey()
	if err != nil {
		return "", err
	}
	raw, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	b, _ := aes.NewCipher(key)
	g, _ := cipher.NewGCM(b)
	if len(raw) < g.NonceSize() {
		return "", errors.New("invalid encrypted webhook secret")
	}
	out, err := g.Open(nil, raw[:g.NonceSize()], raw[g.NonceSize():], []byte("task-webhook-secret-v1"))
	return string(out), err
}
func TaskWebhookSignature(secret, timestamp string, body []byte) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(timestamp + "."))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

func PublicWebhookIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, s := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "2001::/32", "2002::/16", "64:ff9b::/96", "64:ff9b:1::/48"} {
		if netip.MustParsePrefix(s).Contains(ip) {
			return false
		}
	}
	return true
}
func ValidateTaskWebhookURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || len(raw) > 2048 || (u.Port() != "" && u.Port() != "443") {
		return nil, errors.New("webhook URL must be public HTTPS on port 443 without credentials or fragment")
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !PublicWebhookIP(ip) {
		return nil, ErrTaskWebhookAddressBlocked
	}
	return u, nil
}

// Resolve and pin the actual dial target on every request. No env proxy and no redirects.
func TaskWebhookHTTPClient() *http.Client {
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 8 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || port != "443" {
			return nil, errors.New("webhook port rejected")
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			return nil, errors.New("webhook DNS lookup failed")
		}
		for _, ip := range ips {
			if !PublicWebhookIP(ip) {
				return nil, ErrTaskWebhookAddressBlocked
			}
		}
		dialer := net.Dialer{Timeout: 3 * time.Second}
		for _, ip := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
		}
		return nil, errors.New("webhook connection failed")
	}
	return &http.Client{Timeout: 10 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

var taskWebhookSendClient = TaskWebhookHTTPClient

func SendTaskWebhook(ctx context.Context, e *model.TaskWebhookEndpoint, eventID, deliveryID string, body []byte) (int, string, error) {
	if _, err := ValidateTaskWebhookURL(e.URL); err != nil {
		return 0, "", err
	}
	secret, err := DecryptTaskWebhookSecret(e.Secret)
	if err != nil {
		return 0, "", errors.New("webhook signing key unavailable")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.URL, bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "APIMaster-Webhooks/1.0")
	req.Header.Set("X-APIMaster-Event-ID", eventID)
	req.Header.Set("X-APIMaster-Delivery-ID", deliveryID)
	req.Header.Set("X-APIMaster-Timestamp", ts)
	req.Header.Set("X-APIMaster-Signature", "v1="+TaskWebhookSignature(secret, ts, body)+",kid="+e.KeyID)
	client := taskWebhookSendClient()
	defer func() {
		if tr, ok := client.Transport.(*http.Transport); ok {
			tr.CloseIdleConnections()
		}
	}()
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, ErrTaskWebhookAddressBlocked) {
			return 0, "", ErrTaskWebhookAddressBlocked
		}
		return 0, "", errors.New("webhook network error or acknowledgment timeout")
	}
	defer resp.Body.Close()
	// Successful acknowledgment is the response status. Do not wait for an unbounded body.
	retry := resp.Header.Get("Retry-After")
	return resp.StatusCode, retry, nil
}

func VerifyTaskWebhookEndpoint(ctx context.Context, e *model.TaskWebhookEndpoint) error {
	if _, err := ValidateTaskWebhookURL(e.URL); err != nil {
		return err
	}
	secret, err := DecryptTaskWebhookSecret(e.Secret)
	if err != nil {
		return errors.New("webhook signing key unavailable")
	}
	challenge := model.NewWebhookID("verify_")
	id := model.NewWebhookID("evt_")
	body, _ := common.Marshal(map[string]any{"id": id, "type": "webhook.endpoint_verification", "data": map[string]string{"challenge": challenge}})
	req, _ := http.NewRequestWithContext(ctx, "POST", e.URL, bytes.NewReader(body))
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-APIMaster-Event-ID", id)
	req.Header.Set("X-APIMaster-Delivery-ID", model.NewWebhookID("dlv_"))
	req.Header.Set("X-APIMaster-Timestamp", ts)
	req.Header.Set("X-APIMaster-Signature", "v1="+TaskWebhookSignature(secret, ts, body)+",kid="+e.KeyID)
	client := taskWebhookSendClient()
	defer func() {
		if tr, ok := client.Transport.(*http.Transport); ok {
			tr.CloseIdleConnections()
		}
	}()
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("webhook verification request failed")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if err != nil || len(raw) > 4096 || resp.StatusCode != 200 {
		return errors.New("verification requires HTTP 200 and challenge JSON")
	}
	var reply struct {
		Challenge string `json:"challenge"`
	}
	if common.Unmarshal(raw, &reply) != nil || reply.Challenge != challenge {
		return errors.New("verification challenge mismatch")
	}
	return nil
}

func TaskWebhookBackoff(attempt int) time.Duration {
	delays := []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour, 4 * time.Hour, 8 * time.Hour}
	if attempt < 1 {
		attempt = 1
	}
	if attempt > len(delays) {
		return 12 * time.Hour
	}
	return delays[attempt-1]
}
func ClaimTaskWebhook(id string, now int64) (*model.TaskWebhookEvent, error) {
	var claimed model.TaskWebhookEvent
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		lease := model.NewWebhookID("dlv_")
		res := tx.Model(&model.TaskWebhookEvent{}).Where("id = ? AND deadline >= ? AND ((status IN ? AND next_at <= ?) OR (status = ? AND lease_until < ?))", id, now, []string{"pending", "retrying"}, now, "in_flight", now).Updates(map[string]any{"status": "in_flight", "lease": lease, "lease_until": now + 30, "attempts": gorm.Expr("attempts + 1")})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		if err := tx.Where("id = ? AND lease = ?", id, lease).First(&claimed).Error; err != nil {
			return err
		}
		return tx.Create(&model.TaskWebhookAttempt{ID: lease, EventID: id, StartedAt: now}).Error
	})
	return &claimed, err
}
func DeliverTaskWebhook(event *model.TaskWebhookEvent) error {
	start := time.Now()
	e, err := model.WebhookEndpointForUser(event.EndpointID, event.UserID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	code := 0
	retryAfter := ""
	status := "retrying"
	safeError := ""
	if err != nil || !e.Enabled || !e.Verified {
		status = "paused"
		safeError = "endpoint unavailable or paused"
	} else {
		code, retryAfter, err = SendTaskWebhook(context.Background(), e, event.ID, event.Lease, []byte(event.Payload))
		if err != nil {
			safeError = err.Error()
			if errors.Is(err, ErrTaskWebhookAddressBlocked) {
				status = "paused"
				if e != nil {
					if err := model.DB.Model(e).Update("enabled", false).Error; err != nil {
						return err
					}
				}
			}
		} else if code >= 200 && code < 300 {
			status = "delivered"
		} else {
			safeError = fmt.Sprintf("endpoint returned HTTP %d", code)
			if code == 410 {
				status = "paused"
				if err := model.DB.Model(e).Update("enabled", false).Error; err != nil {
					return err
				}
			}
		}
	}
	now := time.Now().Unix()
	next := now + int64(TaskWebhookBackoff(event.Attempts)/time.Second)
	// Jitter uses cryptographic randomness without sharing global PRNG state.
	var jitter [1]byte
	_, _ = rand.Read(jitter[:])
	next += int64(float64(next-now) * (float64(jitter[0])/255*0.4 - 0.2))
	if code == 429 || code == 503 {
		if secs, err := strconv.ParseInt(retryAfter, 10, 64); err == nil && secs > 0 && secs <= 72*3600 {
			if now+secs > next {
				next = now + secs
			}
		} else if at, err := http.ParseTime(retryAfter); err == nil && at.Unix() > next {
			next = at.Unix()
		}
	}
	if next > event.Deadline {
		next = event.Deadline
	}
	if now >= event.Deadline && status != "delivered" {
		status = "exhausted"
	}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		changes := map[string]any{"status": status, "next_at": next, "lease": "", "lease_until": 0, "last_http": code, "last_error": safeError}
		if status == "delivered" {
			changes["delivered_at"] = now
		}
		res := tx.Model(&model.TaskWebhookEvent{}).Where("id = ? AND lease = ?", event.ID, event.Lease).Updates(changes)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}
		return tx.Model(&model.TaskWebhookAttempt{}).Where("id = ?", event.Lease).Updates(map[string]any{"duration_ms": time.Since(start).Milliseconds(), "http_status": code, "error": safeError}).Error
	})
}

var taskWebhookWorkerOnce sync.Once
var taskWebhookLastCleanup atomic.Int64

func StartTaskWebhookWorker() {
	if !common.IsMasterNode {
		return
	}
	taskWebhookWorkerOnce.Do(func() {
		go func() {
			tick := time.NewTicker(time.Second)
			defer tick.Stop()
			for range tick.C {
				if err := RunTaskWebhookDeliveryBatch(); err != nil {
					common.SysError("task webhook worker: " + err.Error())
				}
			}
		}()
	})
}
func RunTaskWebhookDeliveryBatch() error {
	now := time.Now().Unix()
	if err := model.DB.Model(&model.TaskWebhookEvent{}).Where("deadline < ? AND status IN ? AND lease_until < ?", now, []string{"pending", "retrying", "paused", "in_flight"}, now).Updates(map[string]any{"status": "exhausted", "lease": "", "lease_until": 0}).Error; err != nil {
		return err
	}
	var due []model.TaskWebhookEvent
	if err := model.DB.Where("deadline >= ? AND ((status IN ? AND next_at <= ?) OR (status = ? AND lease_until < ?))", now, []string{"pending", "retrying"}, now, "in_flight", now).Order("next_at, created_at").Limit(200).Find(&due).Error; err != nil {
		return err
	}
	seen := map[string]int{}
	users := map[int]int{}
	var wg sync.WaitGroup
	claimedCount := 0
	for _, candidate := range due {
		if seen[candidate.EndpointID] >= 2 || users[candidate.UserID] >= 10 {
			continue
		}
		if claimedCount >= 20 {
			break
		}
		event, err := ClaimTaskWebhook(candidate.ID, now)
		if err == gorm.ErrRecordNotFound {
			continue
		}
		if err != nil {
			return err
		}
		claimedCount++
		seen[candidate.EndpointID]++
		users[candidate.UserID]++
		wg.Add(1)
		go func(e *model.TaskWebhookEvent) {
			defer wg.Done()
			if err := DeliverTaskWebhook(e); err != nil {
				common.SysError("task webhook result persistence failed: " + err.Error())
			}
		}(event)
	}
	wg.Wait()
	if previous := taskWebhookLastCleanup.Load(); now-previous >= 300 && taskWebhookLastCleanup.CompareAndSwap(previous, now) {
		if err := CleanupTaskWebhooks(now); err != nil {
			taskWebhookLastCleanup.Store(previous)
			return err
		}
	}
	return nil
}
func CleanupTaskWebhooks(now int64) error {
	if err := model.DB.Model(&model.TaskWebhookEndpoint{}).Where("previous_until > 0 AND previous_until < ?", now).Updates(map[string]any{"previous_secret": "", "previous_key_id": "", "previous_until": 0}).Error; err != nil {
		return err
	}
	var old []model.TaskWebhookEvent
	if err := model.DB.Where("created_at < ? AND lease_until < ?", now-30*86400, now).Limit(100).Find(&old).Error; err != nil {
		return err
	}
	for _, e := range old {
		if err := model.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Where("event_id = ?", e.ID).Delete(&model.TaskWebhookAttempt{}).Error; err != nil {
				return err
			}
			return tx.Where("id = ? AND lease_until < ?", e.ID, now).Delete(&model.TaskWebhookEvent{}).Error
		}); err != nil {
			return err
		}
	}
	return nil
}

func TaskWebhookModelSupported(name string) bool {
	lower := strings.ToLower(name)
	for _, prefix := range []string{"seedance-", "doubao-seedance-", "sora", "kling-", "minimax-h3", "grok-imagine-video", "grok-1.5-video", "gpt-image-", "gemini-3.1-flash-image", "gemini-3-pro-image", "midjourney-", "suno_", "wan", "veo", "vidu", "hailuo", "jimeng"} {
		if strings.HasPrefix(lower, prefix) {
			return !strings.HasSuffix(lower, "-fd")
		}
	}
	return false
}

func TaskWebhookConfigFromContext(c *gin.Context) *model.TaskWebhookConfig {
	v, ok := c.Get(TaskWebhookContextKey)
	if !ok {
		return nil
	}
	config, _ := v.(*model.TaskWebhookConfig)
	return config
}

func SignedMediaTaskWebhookBase() string {
	base := MediaTaskWebhookBase()
	if base == "" {
		return ""
	}
	key, err := TaskWebhookEncryptionKey()
	if err != nil {
		return base
	}
	return base + "?apimaster_callback_token=" + common.GenerateHMACWithKey(key, "media-task-callback-v1")
}
func ValidMediaTaskWebhookToken(token string) bool {
	key, err := TaskWebhookEncryptionKey()
	if err != nil || token == "" {
		return false
	}
	expected := common.GenerateHMACWithKey(key, "media-task-callback-v1")
	return hmac.Equal([]byte(token), []byte(expected))
}
