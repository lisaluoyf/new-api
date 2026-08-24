package controller

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/gin-gonic/gin"
)

const (
	maxClientErrorFieldLen = 500
	maxClientErrorStackLen = 4000
	maxClientErrorJSONLen  = 2000
)

type clientErrorReportPayload struct {
	Source     string         `json:"source"`
	Route      string         `json:"route"`
	Href       string         `json:"href"`
	Name       string         `json:"name"`
	Message    string         `json:"message"`
	Stack      string         `json:"stack"`
	Digest     string         `json:"digest"`
	Status     int            `json:"status"`
	RequestURL string         `json:"request_url"`
	Method     string         `json:"method"`
	Details    map[string]any `json:"details"`
}

func trimClientErrorField(value string, maxLen int) string {
	value = strings.TrimSpace(value)
	if maxLen <= 0 || len(value) <= maxLen {
		return value
	}
	return value[:maxLen]
}

func ReportClientError(c *gin.Context) {
	userId := c.GetInt("id")
	if userId <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "unauthorized",
		})
		return
	}

	var payload clientErrorReportPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		common.ApiError(c, err)
		return
	}

	source := trimClientErrorField(payload.Source, maxClientErrorFieldLen)
	route := trimClientErrorField(payload.Route, maxClientErrorFieldLen)
	href := trimClientErrorField(payload.Href, maxClientErrorFieldLen)
	name := trimClientErrorField(payload.Name, maxClientErrorFieldLen)
	message := trimClientErrorField(payload.Message, maxClientErrorFieldLen)
	stack := trimClientErrorField(payload.Stack, maxClientErrorStackLen)
	digest := trimClientErrorField(payload.Digest, maxClientErrorFieldLen)
	requestURL := trimClientErrorField(payload.RequestURL, maxClientErrorFieldLen)
	method := trimClientErrorField(payload.Method, 32)
	detailsJSON := trimClientErrorField(common.GetJsonString(payload.Details), maxClientErrorJSONLen)
	userAgent := trimClientErrorField(c.GetHeader("User-Agent"), maxClientErrorFieldLen)
	clientIP := trimClientErrorField(c.ClientIP(), 64)

	logger.LogError(
		c.Request.Context(),
		fmt.Sprintf(
			"panel client error user_id=%d client_ip=%s source=%q route=%q href=%q status=%d request_url=%q method=%q name=%q message=%q digest=%q ua=%q details=%s stack=%q",
			userId,
			clientIP,
			source,
			route,
			href,
			payload.Status,
			requestURL,
			method,
			name,
			message,
			digest,
			userAgent,
			detailsJSON,
			stack,
		),
	)

	common.ApiSuccess(c, gin.H{"accepted": true})
}
