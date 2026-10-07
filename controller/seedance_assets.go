package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func seedanceLibraryError(c *gin.Context, err error) {
	status, message := http.StatusInternalServerError, "Unable to process media library request"
	code := "media_library_error"
	var apiError *service.SeedanceAPIError
	if errors.As(err, &apiError) {
		status, message = apiError.Status, apiError.Message
		code = apiError.Code
	}
	c.JSON(status, gin.H{"error": gin.H{"message": message, "type": "media_library_error", "code": code, "trace_id": c.GetString(common.RequestIdKey)}})
}

func SubmitSeedanceAssets(c *gin.Context) {
	var input service.SeedanceAssetSubmission
	if err := common.UnmarshalBodyReusable(c, &input); err != nil {
		c.JSON(400, gin.H{"error": gin.H{"message": "Invalid JSON request", "type": "invalid_request_error"}})
		return
	}
	task, err := service.SubmitSeedanceAssets(c, input)
	if err != nil {
		seedanceLibraryError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "data": service.SeedanceTaskDTO(task)})
}

func FetchSeedanceAssetTask(c *gin.Context) {
	task, err := model.GetSeedanceResource(c.GetInt("id"), "task", c.Param("task_id"))
	if err != nil {
		c.JSON(404, gin.H{"error": gin.H{"message": "Task not found", "type": "not_found"}})
		return
	}
	if err := service.PollSeedanceAssetTask(c, task); err != nil {
		seedanceLibraryError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "data": service.SeedanceTaskDTO(task)})
}

func CreateSeedanceAssetGroup(c *gin.Context) {
	var input struct {
		service.SeedanceGroupInput
		Model string `json:"model"`
	}
	if err := common.UnmarshalBodyReusable(c, &input); err != nil {
		c.JSON(400, gin.H{"error": gin.H{"message": "Invalid JSON request"}})
		return
	}
	if input.Model == "" {
		input.Model = "seedance-2.5"
	}
	group, err := service.CreateSeedanceGroup(c, input.Model, input.SeedanceGroupInput)
	if err != nil {
		seedanceLibraryError(c, err)
		return
	}
	c.JSON(200, gin.H{"code": 200, "data": service.SeedanceGroupDTO(group)})
}

func seedanceResourceKind(c *gin.Context) string {
	if c.GetString("seedance_resource_kind") == "group" {
		return "group"
	}
	return "asset"
}

func ListSeedanceResources(c *gin.Context) {
	kind := seedanceResourceKind(c)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page < 1 || page > 1000000 || limit < 1 || limit > 100 {
		c.JSON(400, gin.H{"error": gin.H{"message": "page must be positive and limit must be between 1 and 100"}})
		return
	}
	query := model.DB.Model(&model.SeedanceResource{}).Where("user_id = ? AND kind = ?", c.GetInt("id"), kind)
	if kind == "asset" {
		if groupID := c.Query("group_id"); groupID != "" {
			if _, err := model.GetSeedanceResource(c.GetInt("id"), "group", groupID); err != nil {
				c.JSON(404, gin.H{"error": gin.H{"message": "Media group not found"}})
				return
			}
			query = query.Where("group_id = ?", groupID)
		}
		if status := c.Query("status"); status != "" {
			query = query.Where("status = ?", status)
		}
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		seedanceLibraryError(c, err)
		return
	}
	var resources []model.SeedanceResource
	if err := query.Order("created_at DESC, id ASC").Offset((page - 1) * limit).Limit(limit).Find(&resources).Error; err != nil {
		seedanceLibraryError(c, err)
		return
	}
	items := make([]any, 0, len(resources))
	for i := range resources {
		if kind == "group" {
			items = append(items, service.SeedanceGroupDTO(&resources[i]))
		} else {
			items = append(items, service.SeedanceAssetDTO(&resources[i]))
		}
	}
	c.JSON(200, gin.H{"code": 200, "data": gin.H{"items": items, "total": count, "page": page, "limit": limit}})
}

func GetSeedanceResource(c *gin.Context) {
	resource, err := model.GetSeedanceResource(c.GetInt("id"), seedanceResourceKind(c), c.Param("resource_id"))
	if err != nil {
		c.JSON(404, gin.H{"error": gin.H{"message": "Media resource not found"}})
		return
	}
	if resource.Kind == "asset" {
		if err := service.RefreshSeedanceAsset(c, resource); err != nil {
			seedanceLibraryError(c, err)
			return
		}
	}
	var data any = service.SeedanceAssetDTO(resource)
	if resource.Kind == "group" {
		data = service.SeedanceGroupDTO(resource)
	}
	c.JSON(200, gin.H{"code": 200, "data": data})
}

func UpdateSeedanceResource(c *gin.Context) {
	resource, err := model.GetSeedanceResource(c.GetInt("id"), seedanceResourceKind(c), c.Param("resource_id"))
	if err != nil {
		c.JSON(404, gin.H{"error": gin.H{"message": "Media resource not found"}})
		return
	}
	var input struct {
		Name        *string `json:"name,omitempty"`
		Description *string `json:"description,omitempty"`
	}
	if err := common.UnmarshalBodyReusable(c, &input); err != nil {
		c.JSON(400, gin.H{"error": gin.H{"message": "Invalid JSON request"}})
		return
	}
	if err := service.UpdateSeedanceResource(c, resource, input.Name, input.Description); err != nil {
		seedanceLibraryError(c, err)
		return
	}
	if input.Name != nil {
		resource.Name = *input.Name
	}
	if input.Description != nil && resource.Kind == "group" {
		resource.Description = *input.Description
	}
	var data any = service.SeedanceAssetDTO(resource)
	if resource.Kind == "group" {
		data = service.SeedanceGroupDTO(resource)
	}
	c.JSON(200, gin.H{"code": 200, "data": data})
}

func DeleteSeedanceResource(c *gin.Context) {
	resource, err := model.GetSeedanceResource(c.GetInt("id"), seedanceResourceKind(c), c.Param("resource_id"))
	if err != nil {
		c.JSON(404, gin.H{"error": gin.H{"message": "Media resource not found"}})
		return
	}
	if err := service.DeleteSeedanceResource(c, resource); err != nil {
		seedanceLibraryError(c, err)
		return
	}
	c.JSON(200, gin.H{"code": 200, "data": gin.H{"id": resource.ID, "deleted": true}})
}
