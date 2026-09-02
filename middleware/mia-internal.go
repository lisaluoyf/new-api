package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

const miaInternalServiceKeyHeader = "X-Mia-Internal-Key"

func IsMiaInternalServiceRequest(c *gin.Context) bool {
	secret := strings.TrimSpace(common.MiaInternalServiceKey)
	provided := strings.TrimSpace(c.GetHeader(miaInternalServiceKeyHeader))
	if secret == "" || len(secret) != len(provided) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(secret)) == 1
}

func RequireMiaInternalService() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !IsMiaInternalServiceRequest(c) {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"code":    "internal_authentication_required",
				"message": "internal authentication required",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}
