package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func CreateSeedanceVerification(c *gin.Context) {
	c.Header("Cache-Control", "no-store, private")
	c.Header("Referrer-Policy", "no-referrer")
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
	c.Header("Cache-Control", "no-store, private")
	c.Header("Referrer-Policy", "no-referrer")
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

func GetSeedancePortraitCapabilities(c *gin.Context) {
	data, err := service.SeedancePortraitCapabilities(c, c.Query("model"))
	if err != nil {
		seedanceLibraryError(c, err)
		return
	}
	c.JSON(200, gin.H{"code": 200, "data": data})
}

func SeedanceVerificationCallback(c *gin.Context) {
	c.Header("Cache-Control", "no-store, private")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	c.Header("X-Content-Type-Options", "nosniff")
	err := service.HandleSeedanceVerificationCallback(c, c.Param("verification_id"), c.Param("state"), c.GetString("seedance_callback_token"), c.GetString("seedance_callback_result"))
	status, message := 200, "认证回跳已处理。请关闭此页，在 APIMaster 查询服务端确认结果。"
	if err != nil {
		status, message = 400, "本次回跳暂无法确认。请在 APIMaster 查询原会话；不要重复创建认证或上传素材。"
	}
	c.Data(status, "text/html; charset=utf-8", []byte("<!doctype html><html lang=zh><meta charset=utf-8><title>APIMaster 本人认证</title><p>"+message+"</p></html>"))
}

func GetSeedancePortraitVideoRequest(c *gin.Context) {
	resource, err := model.GetSeedanceResource(c.GetInt("id"), "video_request", c.Param("request_id"))
	if err != nil {
		c.JSON(404, gin.H{"error": gin.H{"message": "Video request not found"}})
		return
	}
	c.JSON(200, gin.H{"code": 200, "data": service.SeedanceVideoRequestDTO(resource)})
}
