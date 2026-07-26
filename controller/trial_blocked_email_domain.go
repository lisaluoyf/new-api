package controller

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func trialBlockedEmailDomainFromUser(c *gin.Context) (string, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, err)
		return "", false
	}
	user, err := model.GetUserById(id, false)
	if err != nil {
		common.ApiError(c, err)
		return "", false
	}
	myRole := c.GetInt("role")
	if myRole <= user.Role && myRole != common.RoleRootUser {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "无权操作同级或更高权限用户",
		})
		return "", false
	}
	parts := strings.Split(strings.TrimSpace(user.Email), "@")
	if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "该用户没有可用邮箱域名",
		})
		return "", false
	}
	return parts[1], true
}

func AdminAddTrialBlockedEmailDomain(c *gin.Context) {
	domain, ok := trialBlockedEmailDomainFromUser(c)
	if !ok {
		return
	}
	normalized, err := model.AddTrialBlockedEmailDomain(domain)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"domain": normalized})
}

func AdminRemoveTrialBlockedEmailDomain(c *gin.Context) {
	domain, ok := trialBlockedEmailDomainFromUser(c)
	if !ok {
		return
	}
	normalized, err := model.RemoveTrialBlockedEmailDomain(domain)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"domain": normalized})
}
