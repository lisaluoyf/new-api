package controller

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

func TelegramBind(c *gin.Context) {
	if !common.TelegramOAuthEnabled {
		returnTelegramBindResult(c, "error", "管理员未开启通过 Telegram 登录以及注册")
		return
	}
	params := c.Request.URL.Query()
	if !checkTelegramAuthorization(params, common.TelegramBotToken) {
		returnTelegramBindResult(c, "error", "无效的请求")
		return
	}
	telegramIDs := params["id"]
	if len(telegramIDs) == 0 || strings.TrimSpace(telegramIDs[0]) == "" {
		returnTelegramBindResult(c, "error", "Telegram 账户信息缺失")
		return
	}
	telegramId := telegramIDs[0]
	if model.IsTelegramIdAlreadyTaken(telegramId) {
		returnTelegramBindResult(c, "error", "该 Telegram 账户已被绑定")
		return
	}

	user := model.User{Id: c.GetInt("id")}
	if err := user.FillUserById(); err != nil {
		returnTelegramBindResult(c, "error", err.Error())
		return
	}
	if user.Id == 0 {
		returnTelegramBindResult(c, "error", "用户已注销")
		return
	}
	user.TelegramId = telegramId
	if err := user.Update(false); err != nil {
		returnTelegramBindResult(c, "error", err.Error())
		return
	}

	returnTelegramBindResult(c, "success", "")
}

// returnTelegramBindResult completes the widget callback without exposing an
// extra profile page. The parent profile tab receives the storage event and
// refreshes its binding state; direct navigations fall back to the profile.
func returnTelegramBindResult(c *gin.Context, status, message string) {
	if c.Query("format") == "json" {
		c.JSON(http.StatusOK, gin.H{
			"success": status == "success",
			"message": message,
		})
		return
	}

	redirectTo := sanitizeTelegramRedirect(c.Query("redirect"))
	payload, err := common.Marshal(gin.H{
		"provider":  "telegram",
		"status":    status,
		"message":   message,
		"timestamp": time.Now().UnixMilli(),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "无法完成绑定"})
		return
	}

	html := `<!doctype html><html><head><meta charset="utf-8"><title>Telegram Binding</title></head><body><script>
try { localStorage.setItem('oauth:binding:result', ` + string(payload) + `); } catch (_) {}
window.close();
setTimeout(function () { if (!window.closed) window.location.replace(` + mustMarshalString(redirectTo) + `); }, 200);
</script></body></html>`
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
}

func mustMarshalString(value string) string {
	payload, err := common.Marshal(value)
	if err != nil {
		return `"/console/personal"`
	}
	return string(payload)
}

type telegramChatMemberResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
	Result      struct {
		Status   string `json:"status"`
		IsMember bool   `json:"is_member"`
	} `json:"result"`
}

var telegramAPIBaseURL = "https://api.telegram.org"
var telegramHTTPClient = &http.Client{Timeout: 10 * time.Second}

func telegramBotAPIURL(method string) string {
	return strings.TrimRight(telegramAPIBaseURL, "/") + "/bot" + common.TelegramBotToken + "/" + method
}

// TelegramGroupStatus is the legacy authenticated alias for the group-only
// verification status endpoint.
func TelegramGroupStatus(c *gin.Context) {
	telegramGroupStatus(c)
}

func getTelegramGroupMembership(ctx context.Context, telegramID string) (string, bool, error) {
	form := strings.NewReader("chat_id=" + url.QueryEscape(strings.TrimSpace(common.TelegramGroupChatID)) +
		"&user_id=" + url.QueryEscape(strings.TrimSpace(telegramID)))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, telegramBotAPIURL("getChatMember"), form)
	if err != nil {
		return "", false, errors.New("Unable to prepare Telegram verification request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := telegramHTTPClient.Do(req)
	if err != nil {
		return "", false, errors.New("Unable to reach Telegram for membership verification")
	}
	defer resp.Body.Close()

	var payload telegramChatMemberResponse
	if err := common.DecodeJson(resp.Body, &payload); err != nil {
		return "", false, errors.New("Invalid response from Telegram")
	}
	if !payload.OK {
		message := payload.Description
		if message == "" {
			message = "Telegram membership verification failed"
		}
		return "", false, errors.New(message)
	}

	status := payload.Result.Status
	joined := isTelegramGroupMember(status, payload.Result.IsMember)
	return status, joined, nil
}

func isTelegramGroupMember(status string, isMember bool) bool {
	return status == "member" || status == "administrator" || status == "creator" ||
		(status == "restricted" && isMember)
}

func TelegramLogin(c *gin.Context) {
	if !common.TelegramOAuthEnabled {
		c.JSON(200, gin.H{
			"message": "管理员未开启通过 Telegram 登录以及注册",
			"success": false,
		})
		return
	}
	params := c.Request.URL.Query()
	if !checkTelegramAuthorization(params, common.TelegramBotToken) {
		c.JSON(200, gin.H{
			"message": "无效的请求",
			"success": false,
		})
		return
	}

	telegramId := params["id"][0]
	user := model.User{TelegramId: telegramId}
	if err := user.FillUserByTelegramId(); err != nil {
		c.JSON(200, gin.H{
			"message": err.Error(),
			"success": false,
		})
		return
	}
	setupLogin(&user, c)
}

func checkTelegramAuthorization(params map[string][]string, token string) bool {
	strs := []string{}
	var hash = ""
	for k, v := range params {
		if len(v) == 0 {
			return false
		}
		if k == "hash" {
			hash = v[0]
			continue
		}
		if k == "redirect" || k == "format" {
			continue
		}
		strs = append(strs, k+"="+v[0])
	}
	if hash == "" || token == "" {
		return false
	}
	sort.Strings(strs)
	var imploded = ""
	for _, s := range strs {
		if imploded != "" {
			imploded += "\n"
		}
		imploded += s
	}
	sha256hash := sha256.New()
	io.WriteString(sha256hash, token)
	hmachash := hmac.New(sha256.New, sha256hash.Sum(nil))
	io.WriteString(hmachash, imploded)
	ss := hex.EncodeToString(hmachash.Sum(nil))
	return hash == ss
}

func sanitizeTelegramRedirect(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "/console/personal"
	}
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.HasPrefix(value, "/\\") {
		return "/console/personal"
	}
	return value
}
