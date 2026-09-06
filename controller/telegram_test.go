package controller

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestIsTelegramGroupMember(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		isMember bool
		want     bool
	}{
		{name: "member", status: "member", want: true},
		{name: "administrator", status: "administrator", want: true},
		{name: "creator", status: "creator", want: true},
		{name: "restricted member", status: "restricted", isMember: true, want: true},
		{name: "restricted non member", status: "restricted", want: false},
		{name: "left", status: "left", want: false},
		{name: "kicked", status: "kicked", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTelegramGroupMember(tt.status, tt.isMember); got != tt.want {
				t.Fatalf("isTelegramGroupMember(%q, %t) = %t, want %t", tt.status, tt.isMember, got, tt.want)
			}
		})
	}
}

func setupTelegramWebhookTest(t *testing.T) {
	t.Helper()
	oldDB := model.DB
	oldBotToken := common.TelegramBotToken
	oldBotName := common.TelegramBotName
	oldGroupID := common.TelegramGroupChatID
	oldGroupURL := common.TelegramGroupURL
	oldWebhookSecret := common.TelegramWebhookSecret
	oldMiaWebhookURL := common.MiaTelegramWebhookURL
	oldMiaServiceKey := common.MiaInternalServiceKey
	oldAPIBaseURL := telegramAPIBaseURL
	oldHTTPClient := telegramHTTPClient
	oldMiaHTTPClient := miaTelegramHTTPClient

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TelegramGroupVerification{}))
	model.DB = db
	common.TelegramBotToken = "test-token"
	common.TelegramBotName = "apimasterai_bot"
	common.TelegramGroupChatID = "@apimasterai"
	common.TelegramGroupURL = "https://t.me/apimasterai"
	common.TelegramWebhookSecret = "test-webhook-secret-0123456789abcd"
	common.MiaTelegramWebhookURL = ""
	common.MiaInternalServiceKey = ""

	t.Cleanup(func() {
		model.DB = oldDB
		common.TelegramBotToken = oldBotToken
		common.TelegramBotName = oldBotName
		common.TelegramGroupChatID = oldGroupID
		common.TelegramGroupURL = oldGroupURL
		common.TelegramWebhookSecret = oldWebhookSecret
		common.MiaTelegramWebhookURL = oldMiaWebhookURL
		common.MiaInternalServiceKey = oldMiaServiceKey
		telegramAPIBaseURL = oldAPIBaseURL
		telegramHTTPClient = oldHTTPClient
		miaTelegramHTTPClient = oldMiaHTTPClient
	})
}

func postTelegramUpdate(t *testing.T, payload string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/telegram/webhook", strings.NewReader(payload))
	req.Header.Set(telegramWebhookSecretHeader, common.TelegramWebhookSecret)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = req
	TelegramWebhook(context)
	return recorder
}

func postTelegramWebhook(t *testing.T, secret, token string, telegramID int64) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := common.Marshal(gin.H{
		"update_id": 9001,
		"message": gin.H{
			"chat": gin.H{"id": telegramID, "type": "private"},
			"from": gin.H{"id": telegramID},
			"text": "/start verify_" + token,
		},
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/telegram/webhook", bytes.NewReader(payload))
	req.Header.Set(telegramWebhookSecretHeader, secret)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = req
	TelegramWebhook(context)
	return recorder
}

func TestTelegramWebhookRejectsInvalidSecretAndHandlesReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupTelegramWebhookTest(t)
	require.NoError(t, model.DB.Create(&model.User{Id: 1, Username: "telegram-webhook-user", AffCode: "telegram-webhook-aff"}).Error)
	_, token, err := model.StartTelegramGroupVerification(1, time.Now())
	require.NoError(t, err)

	sendCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/bottest-token/sendMessage", r.URL.Path)
		sendCount++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer server.Close()
	telegramAPIBaseURL = server.URL
	telegramHTTPClient = server.Client()

	recorder := postTelegramWebhook(t, "wrong-secret", token, 10001)
	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.Zero(t, sendCount)

	recorder = postTelegramWebhook(t, common.TelegramWebhookSecret, token, 10001)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, sendCount)
	var user model.User
	require.NoError(t, model.DB.First(&user, 1).Error)
	require.Empty(t, user.TelegramId)

	recorder = postTelegramWebhook(t, common.TelegramWebhookSecret, token, 10001)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, sendCount)
}

func TestTelegramWebhookForwardsNonVerificationUpdatesToMia(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupTelegramWebhookTest(t)

	forwardCount := 0
	miaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwardCount++
		require.Equal(t, "test-mia-service-secret", r.Header.Get(miaInternalServiceKeyHeader))
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.JSONEq(t, `{
			"update_id":9002,
			"message":{
				"chat":{"id":-10001,"type":"supergroup"},
				"from":{"id":10001},
				"text":"@apimasterai_bot hi",
				"entities":[{"type":"mention","offset":0,"length":16}]
			}
		}`, string(body))
		w.WriteHeader(http.StatusAccepted)
	}))
	defer miaServer.Close()
	common.MiaTelegramWebhookURL = miaServer.URL
	common.MiaInternalServiceKey = "test-mia-service-secret"
	miaTelegramHTTPClient = miaServer.Client()

	recorder := postTelegramUpdate(t, `{
		"update_id":9002,
		"message":{
			"chat":{"id":-10001,"type":"supergroup"},
			"from":{"id":10001},
			"text":"@apimasterai_bot hi",
			"entities":[{"type":"mention","offset":0,"length":16}]
		}
	}`)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, forwardCount)
}

func TestTelegramWebhookForwardsCallbackAndInlineUpdatesToMia(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name    string
		payload string
	}{
		{
			name: "callback query",
			payload: `{
				"update_id":9003,
				"callback_query":{
					"id":"callback-1",
					"from":{"id":10001},
					"message":{"message_id":42,"chat":{"id":-10001,"type":"supergroup"}},
					"data":"media:continue_edit:2"
				}
			}`,
		},
		{
			name: "inline query",
			payload: `{
				"update_id":9004,
				"inline_query":{
					"id":"inline-1",
					"from":{"id":10001},
					"query":"share-token",
					"offset":""
				}
			}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupTelegramWebhookTest(t)

			forwardCount := 0
			miaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				forwardCount++
				require.Equal(t, "test-mia-service-secret", r.Header.Get(miaInternalServiceKeyHeader))
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				require.Equal(t, tt.payload, string(body))
				w.WriteHeader(http.StatusAccepted)
			}))
			defer miaServer.Close()
			common.MiaTelegramWebhookURL = miaServer.URL
			common.MiaInternalServiceKey = "test-mia-service-secret"
			miaTelegramHTTPClient = miaServer.Client()

			recorder := postTelegramUpdate(t, tt.payload)
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Equal(t, 1, forwardCount)
		})
	}
}

func TestTelegramWebhookDoesNotForwardVerificationCommandsToMia(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupTelegramWebhookTest(t)
	require.NoError(t, model.DB.Create(&model.User{Id: 1, Username: "telegram-forward-test", AffCode: "telegram-forward-aff"}).Error)
	_, token, err := model.StartTelegramGroupVerification(1, time.Now())
	require.NoError(t, err)

	forwardCount := 0
	miaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwardCount++
		w.WriteHeader(http.StatusAccepted)
	}))
	defer miaServer.Close()
	common.MiaTelegramWebhookURL = miaServer.URL
	common.MiaInternalServiceKey = "test-mia-service-secret"
	miaTelegramHTTPClient = miaServer.Client()

	telegramServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer telegramServer.Close()
	telegramAPIBaseURL = telegramServer.URL
	telegramHTTPClient = telegramServer.Client()

	recorder := postTelegramWebhook(t, common.TelegramWebhookSecret, token, 10001)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Zero(t, forwardCount)
}

func TestIsValidTelegramWebhookSecret(t *testing.T) {
	require.True(t, isValidTelegramWebhookSecret("0123456789abcdef0123456789ABCDEF"))
	require.False(t, isValidTelegramWebhookSecret("too-short"))
	require.False(t, isValidTelegramWebhookSecret("0123456789abcdef0123456789abcde!"))
}

func TestTelegramStatusDoesNotTrustLegacyUserTelegramID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupTelegramWebhookTest(t)
	require.NoError(t, model.DB.Create(&model.User{
		Id: 1, Username: "legacy-telegram-user", AffCode: "legacy-telegram-aff", TelegramId: "10001",
	}).Error)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/user/telegram-verification/status", nil)
	context.Set("id", 1)
	TelegramGroupVerificationStatus(context)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Identified bool   `json:"identified"`
			Status     string `json:"status"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.False(t, response.Data.Identified)
	require.Equal(t, "not_started", response.Data.Status)
}

func TestUnbindTelegramGroupVerificationRequiresAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupTelegramWebhookTest(t)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodDelete, "/api/user/telegram-verification", nil)
	UnbindTelegramGroupVerification(context)
	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestSetupTelegramWebhookUsesConfiguredSecret(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupTelegramWebhookTest(t)
	oldServerAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://apimaster.ai"
	t.Cleanup(func() { system_setting.ServerAddress = oldServerAddress })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/bottest-token/setWebhook", r.URL.Path)
		var payload telegramSetWebhookRequest
		require.NoError(t, common.DecodeJson(r.Body, &payload))
		require.Equal(t, "https://apimaster.ai/api/telegram/webhook", payload.URL)
		require.Equal(t, common.TelegramWebhookSecret, payload.SecretToken)
		require.Equal(t, []string{
			"message",
			"edited_message",
			"callback_query",
			"inline_query",
		}, payload.AllowedUpdates)
		require.True(t, payload.DropPendingUpdates)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer server.Close()
	telegramAPIBaseURL = server.URL
	telegramHTTPClient = server.Client()

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/telegram/webhook/setup",
		strings.NewReader(`{"drop_pending_updates":true}`),
	)
	SetupTelegramWebhook(context)
	require.Equal(t, http.StatusOK, recorder.Code)
}

func TestCheckTelegramAuthorizationIgnoresResponseControls(t *testing.T) {
	token := "test-token"
	dataCheckString := "auth_date=1787198400\nfirst_name=Lisa\nid=12345"
	secret := sha256.Sum256([]byte(token))
	signature := hmac.New(sha256.New, secret[:])
	_, _ = signature.Write([]byte(dataCheckString))
	hash := hex.EncodeToString(signature.Sum(nil))

	params := map[string][]string{
		"auth_date":  {"1787198400"},
		"first_name": {"Lisa"},
		"format":     {"json"},
		"hash":       {hash},
		"id":         {"12345"},
		"redirect":   {"/_panel/profile"},
	}
	if !checkTelegramAuthorization(params, token) {
		t.Fatal("expected valid Telegram signature with response-only controls")
	}
}

func TestCheckTelegramAuthorizationRejectsMissingValues(t *testing.T) {
	if checkTelegramAuthorization(map[string][]string{"hash": {}}, "token") {
		t.Fatal("expected malformed Telegram parameters to be rejected")
	}
}
