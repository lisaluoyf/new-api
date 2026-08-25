package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

const discordCommunityAPIBaseURL = "https://discord.com/api/v10"

var discordCommunityHTTPClient = &http.Client{Timeout: 10 * time.Second}
var discordCommunityAPIURL = discordCommunityAPIBaseURL

type discordCommunityConfig struct {
	BotToken  string
	GuildID   string
	InviteURL string
}

func getDiscordCommunityConfig() discordCommunityConfig {
	return discordCommunityConfig{
		BotToken:  strings.TrimSpace(os.Getenv("DISCORD_COMMUNITY_BOT_TOKEN")),
		GuildID:   strings.TrimSpace(os.Getenv("DISCORD_COMMUNITY_GUILD_ID")),
		InviteURL: strings.TrimSpace(os.Getenv("DISCORD_COMMUNITY_INVITE_URL")),
	}
}

func (config discordCommunityConfig) valid() bool {
	if config.BotToken == "" || config.GuildID == "" || config.InviteURL == "" {
		return false
	}
	parsed, err := url.Parse(config.InviteURL)
	if err != nil || parsed.Scheme != "https" {
		return false
	}
	if _, err := strconv.ParseUint(config.GuildID, 10, 64); err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "discord.gg" || host == "discord.com" || host == "www.discord.com"
}

func discordGroupVerificationConfigured() bool {
	return getDiscordCommunityConfig().valid()
}

func StartDiscordGroupVerification(c *gin.Context) {
	config := getDiscordCommunityConfig()
	if !config.valid() {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": "Discord community verification is not configured",
			"data": gin.H{
				"configured": false,
				"status":     "service_unavailable",
			},
		})
		return
	}

	user, err := getDiscordVerificationUser(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	bound := strings.TrimSpace(user.DiscordId) != ""
	data := gin.H{
		"configured": true,
		"bound":      bound,
		"joined":     false,
		"status":     "binding_required",
	}
	if bound {
		data["status"] = "not_joined"
		data["invite_url"] = config.InviteURL
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

func DiscordGroupVerificationStatus(c *gin.Context) {
	config := getDiscordCommunityConfig()
	user, err := getDiscordVerificationUser(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	bound := strings.TrimSpace(user.DiscordId) != ""
	if !config.valid() {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
			"configured": false,
			"bound":      bound,
			"joined":     false,
			"status":     "service_unavailable",
		}})
		return
	}
	if !bound {
		if err := model.RecordDiscordGroupVerification(user.Id, config.GuildID, false, time.Now()); err != nil {
			common.SysError("failed to invalidate Discord group verification status: " + err.Error())
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
			"configured": true,
			"bound":      false,
			"joined":     false,
			"status":     "binding_required",
		}})
		return
	}

	joined, err := getDiscordGuildMembership(c.Request.Context(), config, user.DiscordId)
	if err != nil {
		common.SysError(fmt.Sprintf("Discord membership lookup failed for user %d: %s", user.Id, err.Error()))
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
			"configured": true,
			"bound":      true,
			"joined":     false,
			"status":     "service_unavailable",
			"invite_url": config.InviteURL,
		}})
		return
	}
	checkedAt := time.Now().UTC()
	if err := model.RecordDiscordGroupVerification(user.Id, config.GuildID, joined, checkedAt); err != nil {
		common.SysError("failed to persist Discord group verification status: " + err.Error())
	}
	status := "not_joined"
	if joined {
		status = "joined"
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"configured": true,
		"bound":      true,
		"joined":     joined,
		"status":     status,
		"invite_url": config.InviteURL,
		"checked_at": checkedAt.Format(time.RFC3339),
	}})
}

func getDiscordVerificationUser(userID int) (*model.User, error) {
	user := &model.User{Id: userID}
	if err := user.FillUserById(); err != nil {
		return nil, err
	}
	return user, nil
}

func getDiscordGuildMembership(ctx context.Context, config discordCommunityConfig, discordID string) (bool, error) {
	endpoint := strings.TrimRight(discordCommunityAPIURL, "/") + "/guilds/" +
		url.PathEscape(config.GuildID) + "/members/" + url.PathEscape(strings.TrimSpace(discordID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bot "+config.BotToken)
	req.Header.Set("Accept", "application/json")
	resp, err := discordCommunityHTTPClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("unable to reach Discord: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return false, fmt.Errorf("Discord community verification credentials were rejected")
	case http.StatusTooManyRequests:
		return false, fmt.Errorf("Discord community verification is temporarily rate limited")
	default:
		return false, fmt.Errorf("Discord membership lookup returned HTTP %d", resp.StatusCode)
	}
}
