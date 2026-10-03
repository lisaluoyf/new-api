package service

import (
	"context"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

func setupTaskWebhookTest(t *testing.T) {
	t.Setenv("TASK_WEBHOOK_MASTER_KEY", "test-only-task-webhook-key-32-bytes-strong")
	require.NoError(t, model.DB.AutoMigrate(&model.TaskWebhookEndpoint{}, &model.TaskWebhookEvent{}, &model.TaskWebhookAttempt{}))
	for _, x := range []any{&model.TaskWebhookEvent{}, &model.TaskWebhookAttempt{}, &model.TaskWebhookEndpoint{}} {
		require.NoError(t, model.DB.Where("1=1").Delete(x).Error)
	}
}
func TestTaskWebhookEncryptionSignature(t *testing.T) {
	setupTaskWebhookTest(t)
	encrypted, err := EncryptTaskWebhookSecret("secret")
	require.NoError(t, err)
	plain, err := DecryptTaskWebhookSecret(encrypted)
	require.NoError(t, err)
	require.Equal(t, "secret", plain)
	require.Equal(t, common.GenerateHMACWithKey([]byte("secret"), "123.body"), TaskWebhookSignature("secret", "123", []byte("body")))
	t.Setenv("TASK_WEBHOOK_MASTER_KEY", "another-key-that-is-at-least-32-bytes")
	_, err = DecryptTaskWebhookSecret(encrypted)
	require.Error(t, err)
}
func TestTaskWebhookSSRF(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "::1", "::ffff:127.0.0.1", "fc00::1", "2001:db8::1", "64:ff9b::a00:1"} {
		require.False(t, PublicWebhookIP(netip.MustParseAddr(ip)), ip)
	}
	for _, u := range []string{"http://example.com", "https://localhost:8443", "https://u:p@example.com", "https://127.0.0.1"} {
		_, err := ValidateTaskWebhookURL(u)
		require.Error(t, err)
	}
}

type webhookTestTransport struct {
	target string
	base   http.RoundTripper
}

func (t webhookTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c := r.Clone(context.Background())
	target, _ := http.NewRequest("GET", t.target, nil)
	c.URL.Scheme = target.URL.Scheme
	c.URL.Host = target.URL.Host
	return t.base.RoundTrip(c)
}
func TestTaskWebhookRetryLeaseRecovery(t *testing.T) {
	setupTaskWebhookTest(t)
	encrypted, err := EncryptTaskWebhookSecret("test-secret")
	require.NoError(t, err)
	calls := 0
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.NotEmpty(t, r.Header.Get("X-APIMaster-Signature"))
		require.Equal(t, "evt_test", r.Header.Get("X-APIMaster-Event-ID"))
		if calls == 1 {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(503)
		} else {
			w.WriteHeader(204)
		}
	}))
	defer s.Close()
	old := taskWebhookSendClient
	t.Cleanup(func() { taskWebhookSendClient = old })
	taskWebhookSendClient = func() *http.Client {
		return &http.Client{Transport: webhookTestTransport{s.URL, s.Client().Transport}, Timeout: 10 * time.Second}
	}
	ep := model.TaskWebhookEndpoint{ID: "ep", UserID: 1, URL: "https://example.com/cb", Secret: encrypted, KeyID: "key", Enabled: true, Verified: true}
	require.NoError(t, model.DB.Create(&ep).Error)
	now := time.Now().Unix()
	e := model.TaskWebhookEvent{ID: "evt_test", Resource: "task:1", UserID: 1, EndpointID: "ep", Payload: `{"id":"evt_test"}`, Status: "pending", NextAt: now, Deadline: now + 3600, CreatedAt: now}
	require.NoError(t, model.DB.Create(&e).Error)
	claimed, err := ClaimTaskWebhook(e.ID, now)
	require.NoError(t, err)
	_, err = ClaimTaskWebhook(e.ID, now)
	require.Error(t, err)
	require.NoError(t, DeliverTaskWebhook(claimed))
	var fresh model.TaskWebhookEvent
	require.NoError(t, model.DB.First(&fresh, "id = ?", e.ID).Error)
	require.Equal(t, "retrying", fresh.Status)
	require.GreaterOrEqual(t, fresh.NextAt, now+120)
	require.NoError(t, model.DB.Model(&fresh).Updates(map[string]any{"status": "in_flight", "lease": "dead", "lease_until": now - 1}).Error)
	claimed, err = ClaimTaskWebhook(e.ID, now)
	require.NoError(t, err)
	require.NotEqual(t, "dead", claimed.Lease)
	require.NoError(t, DeliverTaskWebhook(claimed))
	require.NoError(t, model.DB.First(&fresh, "id = ?", e.ID).Error)
	require.Equal(t, "delivered", fresh.Status)
	require.Equal(t, 2, calls)
}

func TestTaskWebhookTimeoutGoneAndExhaustion(t *testing.T) {
	setupTaskWebhookTest(t)
	encrypted, err := EncryptTaskWebhookSecret("test-secret")
	require.NoError(t, err)
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/timeout" {
			time.Sleep(80 * time.Millisecond)
			w.WriteHeader(204)
			return
		}
		w.WriteHeader(410)
	}))
	defer s.Close()
	old := taskWebhookSendClient
	t.Cleanup(func() { taskWebhookSendClient = old })
	taskWebhookSendClient = func() *http.Client {
		return &http.Client{Transport: webhookTestTransport{s.URL, s.Client().Transport}, Timeout: 20 * time.Millisecond}
	}
	ep := model.TaskWebhookEndpoint{ID: "ep", UserID: 1, URL: "https://example.com/timeout", Secret: encrypted, KeyID: "key", Enabled: true, Verified: true}
	require.NoError(t, model.DB.Create(&ep).Error)
	now := time.Now().Unix()
	e := model.TaskWebhookEvent{ID: "evt_timeout", Resource: "timeout", UserID: 1, EndpointID: ep.ID, Payload: `{"id":"evt_timeout"}`, Status: "pending", NextAt: now, Deadline: now + 3600, CreatedAt: now}
	require.NoError(t, model.DB.Create(&e).Error)
	claimed, err := ClaimTaskWebhook(e.ID, now)
	require.NoError(t, err)
	require.NoError(t, DeliverTaskWebhook(claimed))
	var fresh model.TaskWebhookEvent
	require.NoError(t, model.DB.First(&fresh, "id = ?", e.ID).Error)
	require.Equal(t, "retrying", fresh.Status)
	require.Equal(t, 0, fresh.LastHTTP)
	require.NoError(t, model.DB.Model(&ep).Update("url", "https://example.com/gone").Error)
	require.NoError(t, model.DB.Model(&fresh).Update("next_at", now).Error)
	claimed, err = ClaimTaskWebhook(e.ID, now)
	require.NoError(t, err)
	require.NoError(t, DeliverTaskWebhook(claimed))
	require.NoError(t, model.DB.First(&fresh, "id = ?", e.ID).Error)
	require.Equal(t, "paused", fresh.Status)
	require.Equal(t, 410, fresh.LastHTTP)
	require.NoError(t, model.DB.First(&ep, "id = ?", ep.ID).Error)
	require.False(t, ep.Enabled)
	require.NoError(t, model.DB.Model(&fresh).Update("deadline", now-1).Error)
	require.NoError(t, RunTaskWebhookDeliveryBatch())
	require.NoError(t, model.DB.First(&fresh, "id = ?", e.ID).Error)
	require.Equal(t, "exhausted", fresh.Status)
}
