package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func CreateSeedanceVerification(c *gin.Context) {
	var input service.SeedanceVerificationInput
	if err := common.UnmarshalBodyReusable(c, &input); err != nil {
		c.JSON(400, gin.H{"error": gin.H{"message": "Invalid JSON request", "type": "invalid_request_error"}})
		return
	}
	resource, err := service.CreateSeedanceVerification(c, input)
	if err != nil {
		seedanceLibraryError(c, err)
		return
	}
	c.JSON(200, gin.H{"code": 200, "data": service.SeedanceVerificationDTO(resource)})
}

func GetSeedanceVerification(c *gin.Context) {
	resource, err := model.GetSeedanceResource(c.GetInt("id"), "verification", c.Param("verification_id"))
	if err != nil {
		c.JSON(404, gin.H{"error": gin.H{"message": "Verification session not found", "type": "not_found"}})
		return
	}
	if err := service.RefreshSeedanceVerification(c, resource); err != nil {
		seedanceLibraryError(c, err)
		return
	}
	c.JSON(200, gin.H{"code": 200, "data": service.SeedanceVerificationDTO(resource)})
}
