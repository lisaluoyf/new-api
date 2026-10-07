package middleware

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

const RouteTagKey = "route_tag"

func RouteTag(tag string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(RouteTagKey, tag)
		c.Next()
	}
}

func SetUpLogger(server *gin.Engine) {
	server.Use(gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		var requestID string
		if param.Keys != nil {
			requestID, _ = param.Keys[common.RequestIdKey].(string)
		}
		tag, _ := param.Keys[RouteTagKey].(string)
		if tag == "" {
			tag = "web"
		}
		if strings.HasPrefix(param.Path, "/v1/seedance2/private-avatar/callback/") || strings.HasPrefix(param.Path, "/v1/seedance2/private-avatar/files/") {
			param.Path = "/v1/seedance2/private-avatar/callback/[redacted]"
		}
		return fmt.Sprintf("[GIN] %s | %s | %s | %3d | %13v | %15s | %7s %s\n",
			param.TimeStamp.Format("2006/01/02 - 15:04:05"),
			tag,
			requestID,
			param.StatusCode,
			param.Latency,
			param.ClientIP,
			param.Method,
			param.Path,
		)
	}))
}

// Run before recovery/logger. Callback parameters are held only in request-local
// memory; panic dumps and loggers cannot serialize the original request URI.
func ProtectSeedanceSensitiveRequest() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/v1/seedance2/private-avatar/callback/") || strings.HasPrefix(path, "/v1/seedance2/private-avatar/files/") {
			c.Set("seedance_callback_token", c.Query("bytedToken"))
			c.Set("seedance_callback_result", c.Query("resultCode"))
			c.Request.URL.RawQuery = ""
			c.Request.RequestURI = "/v1/seedance2/private-avatar/[redacted]"
		}
		c.Next()
	}
}
