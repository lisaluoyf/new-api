package middleware

import (
	"net/http"
	"net/url"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

type turnstileCheckResponse struct {
	Success bool `json:"success"`
}

func TurnstileCheck() gin.HandlerFunc {
	return func(c *gin.Context) {
		if common.TurnstileCheckEnabled {
			session := sessions.Default(c)
			turnstileChecked := session.Get("turnstile")
			if turnstileChecked != nil {
				c.Next()
				return
			}
			response := c.Query("turnstile")
			if response == "" {
				common.ApiErrorI18n(c, i18n.MsgTurnstileTokenEmpty)
				c.Abort()
				return
			}
			rawRes, err := http.PostForm("https://challenges.cloudflare.com/turnstile/v0/siteverify", url.Values{
				"secret":   {common.TurnstileSecretKey},
				"response": {response},
				"remoteip": {c.ClientIP()},
			})
			if err != nil {
				common.SysLog(err.Error())
				common.ApiErrorI18n(c, i18n.MsgTurnstileUnavailable)
				c.Abort()
				return
			}
			defer rawRes.Body.Close()
			var res turnstileCheckResponse
			err = common.DecodeJson(rawRes.Body, &res)
			if err != nil {
				common.SysLog(err.Error())
				common.ApiErrorI18n(c, i18n.MsgTurnstileUnavailable)
				c.Abort()
				return
			}
			if !res.Success {
				common.ApiErrorI18n(c, i18n.MsgTurnstileVerificationFailed)
				c.Abort()
				return
			}
			session.Set("turnstile", true)
			err = session.Save()
			if err != nil {
				common.ApiErrorI18n(c, i18n.MsgUserSessionSaveFailed)
				c.Abort()
				return
			}
		}
		c.Next()
	}
}
