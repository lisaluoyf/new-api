package common

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type feishuCardTestTransport func(*http.Request) (*http.Response, error)

func (transport feishuCardTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestSendFeishuInteractiveCard(t *testing.T) {
	t.Setenv("FEISHU_APP_ID", "test-app")
	t.Setenv("FEISHU_APP_SECRET", "test-secret")
	previousTransport := http.DefaultTransport
	previousToken, previousExpiry := feishuCachedToken, feishuTokenExpiry
	feishuCachedToken, feishuTokenExpiry = "test-token", time.Now().Add(time.Hour)
	t.Cleanup(func() {
		http.DefaultTransport = previousTransport
		feishuCachedToken, feishuTokenExpiry = previousToken, previousExpiry
	})
	responseBody := `{"code":0}`
	http.DefaultTransport = feishuCardTestTransport(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.Query().Get("receive_id_type") != "chat_id" {
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL)
		}
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatal("missing authentication")
		}
		var payload struct {
			ReceiveID   string `json:"receive_id"`
			MessageType string `json:"msg_type"`
			Content     string `json:"content"`
		}
		if err := DecodeJson(request.Body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.ReceiveID != "test-chat" || payload.MessageType != "interactive" {
			t.Fatalf("unexpected destination: %+v", payload)
		}
		var card map[string]any
		if err := Unmarshal([]byte(payload.Content), &card); err != nil {
			t.Fatal(err)
		}
		if card["schema"] != "2.0" || card["header"].(map[string]any)["template"] != "orange" {
			t.Fatalf("card changed during delivery: %+v", card)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(responseBody))}, nil
	})
	card := map[string]any{"schema": "2.0", "header": map[string]any{"template": "orange"}}
	if err := SendFeishuInteractiveCard("test-chat", card); err != nil {
		t.Fatal(err)
	}
	responseBody = `{"code":230099,"msg":"invalid card"}`
	if err := SendFeishuInteractiveCard("test-chat", card); err == nil {
		t.Fatal("expected rejected cards to remain retryable")
	}
	if err := SendFeishuInteractiveCard("test-chat", map[string]any{"invalid": make(chan int)}); err == nil {
		t.Fatal("expected an invalid card to fail serialization")
	}
}
