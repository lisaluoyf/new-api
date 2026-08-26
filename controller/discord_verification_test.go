package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupDiscordVerificationTest(t *testing.T, discordID string, statusCode int) {
	t.Helper()
	oldDB := model.DB
	oldAPIURL := discordCommunityAPIURL
	oldHTTPClient := discordCommunityHTTPClient
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.DiscordGroupVerification{}))
	model.DB = db
	require.NoError(t, model.DB.Create(&model.User{
		Id: 1, Username: "discord-user", AffCode: "discord-aff", DiscordId: discordID,
	}).Error)
	t.Setenv("DISCORD_COMMUNITY_BOT_TOKEN", "test-bot-token")
	t.Setenv("DISCORD_COMMUNITY_GUILD_ID", "123456789012345678")
	t.Setenv("DISCORD_COMMUNITY_INVITE_URL", "https://discord.gg/apimaster")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/guilds/123456789012345678/members/discord-1", r.URL.Path)
		require.Equal(t, "Bot test-bot-token", r.Header.Get("Authorization"))
		w.WriteHeader(statusCode)
	}))
	discordCommunityAPIURL = server.URL
	discordCommunityHTTPClient = server.Client()
	t.Cleanup(func() {
		server.Close()
		model.DB = oldDB
		discordCommunityAPIURL = oldAPIURL
		discordCommunityHTTPClient = oldHTTPClient
	})
}

func discordStatusRequest(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/user/discord-verification/status", nil)
	context.Set("id", 1)
	DiscordGroupVerificationStatus(context)
	return recorder
}

func TestDiscordStatusRequiresBindingBeforeMembershipLookup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupDiscordVerificationTest(t, "", http.StatusOK)
	recorder := discordStatusRequest(t)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Bound     bool   `json:"bound"`
			Joined    bool   `json:"joined"`
			Status    string `json:"status"`
			DiscordID string `json:"discord_id"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.False(t, response.Data.Bound)
	require.False(t, response.Data.Joined)
	require.Equal(t, "binding_required", response.Data.Status)
	require.Empty(t, response.Data.DiscordID)
}

func TestDiscordStatusPersistsJoinedMembership(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupDiscordVerificationTest(t, "discord-1", http.StatusOK)
	recorder := discordStatusRequest(t)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Joined    bool   `json:"joined"`
			Status    string `json:"status"`
			DiscordID string `json:"discord_id"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.True(t, response.Data.Joined)
	require.Equal(t, "joined", response.Data.Status)
	require.Equal(t, "discord-1", response.Data.DiscordID)
	verification, err := model.GetDiscordGroupVerification(1, "123456789012345678")
	require.NoError(t, err)
	require.NotNil(t, verification.JoinedAt)
}

func TestDiscordStatusTreatsNotFoundAsNotJoined(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupDiscordVerificationTest(t, "discord-1", http.StatusNotFound)
	recorder := discordStatusRequest(t)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Data struct {
			Joined bool   `json:"joined"`
			Status string `json:"status"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.False(t, response.Data.Joined)
	require.Equal(t, "not_joined", response.Data.Status)
}

func TestDiscordStatusDoesNotTreatAPIFailureAsMembership(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupDiscordVerificationTest(t, "discord-1", http.StatusForbidden)
	recorder := discordStatusRequest(t)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Data struct {
			Configured bool   `json:"configured"`
			Joined     bool   `json:"joined"`
			Status     string `json:"status"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Data.Configured)
	require.False(t, response.Data.Joined)
	require.Equal(t, "service_unavailable", response.Data.Status)
}

func TestDiscordStatusReportsUnavailableConfiguration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupDiscordVerificationTest(t, "discord-1", http.StatusOK)
	t.Setenv("DISCORD_COMMUNITY_BOT_TOKEN", "")
	recorder := discordStatusRequest(t)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Data struct {
			Configured bool   `json:"configured"`
			Status     string `json:"status"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.False(t, response.Data.Configured)
	require.Equal(t, "service_unavailable", response.Data.Status)
}
