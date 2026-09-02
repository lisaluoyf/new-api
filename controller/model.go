package controller

import (
	"fmt"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relay/channel/ai360"
	"github.com/QuantumNous/new-api/relay/channel/lingyiwanwu"
	"github.com/QuantumNous/new-api/relay/channel/minimax"
	"github.com/QuantumNous/new-api/relay/channel/moonshot"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

// https://platform.openai.com/docs/api-reference/models/list

var openAIModels []dto.OpenAIModels
var openAIModelsMap map[string]dto.OpenAIModels
var channelId2Models map[int][]string

func init() {
	// https://platform.openai.com/docs/models/model-endpoint-compatibility
	for i := 0; i < constant.APITypeDummy; i++ {
		if i == constant.APITypeAIProxyLibrary {
			continue
		}
		adaptor := relay.GetAdaptor(i)
		channelName := adaptor.GetChannelName()
		modelNames := adaptor.GetModelList()
		for _, modelName := range modelNames {
			openAIModels = append(openAIModels, dto.OpenAIModels{
				Id:      modelName,
				Object:  "model",
				Created: 1626777600,
				OwnedBy: channelName,
			})
		}
	}
	for _, modelName := range ai360.ModelList {
		openAIModels = append(openAIModels, dto.OpenAIModels{
			Id:      modelName,
			Object:  "model",
			Created: 1626777600,
			OwnedBy: ai360.ChannelName,
		})
	}
	for _, modelName := range moonshot.ModelList {
		openAIModels = append(openAIModels, dto.OpenAIModels{
			Id:      modelName,
			Object:  "model",
			Created: 1626777600,
			OwnedBy: moonshot.ChannelName,
		})
	}
	for _, modelName := range lingyiwanwu.ModelList {
		openAIModels = append(openAIModels, dto.OpenAIModels{
			Id:      modelName,
			Object:  "model",
			Created: 1626777600,
			OwnedBy: lingyiwanwu.ChannelName,
		})
	}
	for _, modelName := range minimax.ModelList {
		openAIModels = append(openAIModels, dto.OpenAIModels{
			Id:      modelName,
			Object:  "model",
			Created: 1626777600,
			OwnedBy: minimax.ChannelName,
		})
	}
	for modelName, _ := range constant.MidjourneyModel2Action {
		openAIModels = append(openAIModels, dto.OpenAIModels{
			Id:      modelName,
			Object:  "model",
			Created: 1626777600,
			OwnedBy: "midjourney",
		})
	}
	openAIModelsMap = make(map[string]dto.OpenAIModels)
	for _, aiModel := range openAIModels {
		openAIModelsMap[aiModel.Id] = aiModel
	}
	channelId2Models = make(map[int][]string)
	for i := 1; i <= constant.ChannelTypeDummy; i++ {
		apiType, success := common.ChannelType2APIType(i)
		if !success || apiType == constant.APITypeAIProxyLibrary {
			continue
		}
		meta := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: i,
		}}
		adaptor := relay.GetAdaptor(apiType)
		adaptor.Init(meta)
		channelId2Models[i] = adaptor.GetModelList()
	}
	openAIModels = lo.UniqBy(openAIModels, func(m dto.OpenAIModels) string {
		return m.Id
	})
}

func ListModels(c *gin.Context, modelType int) {
	userOpenAiModels, err := GetAccessibleOpenAIModels(c)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "get user group failed",
		})
		return
	}

	switch modelType {
	case constant.ChannelTypeAnthropic:
		useranthropicModels := make([]dto.AnthropicModel, len(userOpenAiModels))
		for i, model := range userOpenAiModels {
			useranthropicModels[i] = dto.AnthropicModel{
				ID:          model.Id,
				CreatedAt:   time.Unix(int64(model.Created), 0).UTC().Format(time.RFC3339),
				DisplayName: model.Id,
				Type:        "model",
			}
		}
		response := gin.H{
			"data":     useranthropicModels,
			"has_more": false,
		}
		if len(useranthropicModels) > 0 {
			response["first_id"] = useranthropicModels[0].ID
			response["last_id"] = useranthropicModels[len(useranthropicModels)-1].ID
		}
		c.JSON(http.StatusOK, response)
	case constant.ChannelTypeGemini:
		userGeminiModels := make([]dto.GeminiModel, len(userOpenAiModels))
		for i, model := range userOpenAiModels {
			userGeminiModels[i] = dto.GeminiModel{
				Name:        model.Id,
				DisplayName: model.Id,
			}
		}
		c.JSON(http.StatusOK, gin.H{
			"models":        userGeminiModels,
			"nextPageToken": nil,
		})
	default:
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data":    userOpenAiModels,
			"object":  "list",
		})
	}
}

// GetAccessibleOpenAIModels returns the same model set exposed by GET /v1/models.
// Keeping this logic shared ensures registry consumers and regular API clients
// observe identical token model limits, user groups, and billing visibility.
func GetAccessibleOpenAIModels(c *gin.Context) ([]dto.OpenAIModels, error) {
	modelLimitEnabled := common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled)
	modelLimit := map[string]bool{}
	if value, ok := common.GetContextKey(c, constant.ContextKeyTokenModelLimit); ok {
		modelLimit, _ = value.(map[string]bool)
	}
	return getAccessibleOpenAIModels(
		c.GetInt("id"),
		common.GetContextKeyString(c, constant.ContextKeyTokenGroup),
		modelLimitEnabled,
		modelLimit,
	)
}

// GetAccessibleOpenAIModelsForToken applies the same visibility rules as
// GET /v1/models without exposing or authenticating with the token key.
func GetAccessibleOpenAIModelsForToken(userID int, token *model.Token) ([]dto.OpenAIModels, error) {
	if token == nil {
		return nil, fmt.Errorf("token is nil")
	}
	models, err := getAccessibleOpenAIModels(
		userID,
		token.Group,
		token.ModelLimitsEnabled,
		token.GetModelLimitsMap(),
	)
	if err != nil || !token.ModelLimitsEnabled {
		return models, err
	}

	// Model-limited keys still have to obey their selected group. The regular
	// relay enforces this at channel selection time; the catalog filters it up
	// front so Mia never offers a model that the key cannot actually route.
	groupModels, err := getTokenGroupEnabledModels(userID, token.Group)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]struct{}, len(groupModels))
	for _, modelName := range groupModels {
		allowed[modelName] = struct{}{}
	}
	filtered := make([]dto.OpenAIModels, 0, len(models))
	for _, accessibleModel := range models {
		if _, ok := allowed[accessibleModel.Id]; ok {
			filtered = append(filtered, accessibleModel)
		}
	}
	return filtered, nil
}

func getTokenGroupEnabledModels(userID int, tokenGroup string) ([]string, error) {
	userGroup, err := model.GetUserGroup(userID, false)
	if err != nil {
		return nil, err
	}
	if tokenGroup == "auto" {
		models := make([]string, 0)
		for _, autoGroup := range service.GetUserAutoGroup(userGroup) {
			for _, modelName := range model.GetGroupEnabledModels(autoGroup) {
				if !common.StringsContains(models, modelName) {
					models = append(models, modelName)
				}
			}
		}
		return models, nil
	}
	if service.IsFreeTrialGroup(tokenGroup) {
		return service.FilterFreeTrialModels(model.GetGroupEnabledModels(service.AutoCheapestGroup)), nil
	}
	if tokenGroup != "" {
		userGroup = tokenGroup
	}
	return model.GetGroupEnabledModels(userGroup), nil
}

func getAccessibleOpenAIModels(userID int, tokenGroup string, modelLimitEnable bool, tokenModelLimit map[string]bool) ([]dto.OpenAIModels, error) {
	userOpenAiModels := make([]dto.OpenAIModels, 0)

	acceptUnsetRatioModel := operation_setting.SelfUseModeEnabled
	if !acceptUnsetRatioModel {
		if userID > 0 {
			userSettings, _ := model.GetUserSetting(userID, false)
			if userSettings.AcceptUnsetRatioModel {
				acceptUnsetRatioModel = true
			}
		}
	}

	if modelLimitEnable {
		if tokenModelLimit == nil {
			tokenModelLimit = map[string]bool{}
		}
		for allowModel, _ := range tokenModelLimit {
			if service.IsFreeModel(allowModel) {
				eligible, _, eligibilityErr := service.FreeModelEligibility(userID)
				if eligibilityErr != nil || !eligible || !common.StringsContains(model.GetEnabledModels(), service.FreeModelID) {
					continue
				}
				userOpenAiModels = append(userOpenAiModels, dto.OpenAIModels{Id: service.FreeModelID, Object: "model", Created: 1626777600, OwnedBy: "apimaster", SupportedEndpointTypes: model.GetModelSupportEndpointTypes(service.FreeModelID)})
				continue
			}
			if !acceptUnsetRatioModel {
				if !helper.HasModelBillingConfig(allowModel) {
					continue
				}
			}
			if oaiModel, ok := openAIModelsMap[allowModel]; ok {
				oaiModel.SupportedEndpointTypes = model.GetModelSupportEndpointTypes(allowModel)
				userOpenAiModels = append(userOpenAiModels, oaiModel)
			} else {
				userOpenAiModels = append(userOpenAiModels, dto.OpenAIModels{
					Id:                     allowModel,
					Object:                 "model",
					Created:                1626777600,
					OwnedBy:                "custom",
					SupportedEndpointTypes: model.GetModelSupportEndpointTypes(allowModel),
				})
			}
		}
	} else {
		userGroup, err := model.GetUserGroup(userID, false)
		if err != nil {
			return nil, err
		}
		group := userGroup
		if tokenGroup != "" {
			group = tokenGroup
		}
		var models []string
		if tokenGroup == "auto" {
			for _, autoGroup := range service.GetUserAutoGroup(userGroup) {
				groupModels := model.GetGroupEnabledModels(autoGroup)
				for _, g := range groupModels {
					if !common.StringsContains(models, g) {
						models = append(models, g)
					}
				}
			}
		} else if service.IsFreeTrialGroup(tokenGroup) {
			models = service.FilterFreeTrialModels(model.GetGroupEnabledModels(service.AutoCheapestGroup))
		} else {
			models = model.GetGroupEnabledModels(group)
		}
		for _, modelName := range models {
			if service.IsFreeModel(modelName) {
				eligible, _, eligibilityErr := service.FreeModelEligibility(userID)
				if eligibilityErr == nil && eligible {
					userOpenAiModels = append(userOpenAiModels, dto.OpenAIModels{Id: service.FreeModelID, Object: "model", Created: 1626777600, OwnedBy: "apimaster", SupportedEndpointTypes: model.GetModelSupportEndpointTypes(service.FreeModelID)})
				}
				continue
			}
			if service.ShouldHideGptImage2OfficialModel(modelName) {
				continue
			}
			if !acceptUnsetRatioModel {
				if !helper.HasModelBillingConfig(modelName) {
					continue
				}
			}
			if oaiModel, ok := openAIModelsMap[modelName]; ok {
				oaiModel.SupportedEndpointTypes = model.GetModelSupportEndpointTypes(modelName)
				userOpenAiModels = append(userOpenAiModels, oaiModel)
			} else {
				userOpenAiModels = append(userOpenAiModels, dto.OpenAIModels{
					Id:                     modelName,
					Object:                 "model",
					Created:                1626777600,
					OwnedBy:                "custom",
					SupportedEndpointTypes: model.GetModelSupportEndpointTypes(modelName),
				})
			}
		}
	}

	return userOpenAiModels, nil
}

func ChannelListModels(c *gin.Context) {
	c.JSON(200, gin.H{
		"success": true,
		"data":    openAIModels,
	})
}

func DashboardListModels(c *gin.Context) {
	c.JSON(200, gin.H{
		"success": true,
		"data":    channelId2Models,
	})
}

func EnabledListModels(c *gin.Context) {
	c.JSON(200, gin.H{
		"success": true,
		"data":    model.GetEnabledModels(),
	})
}

func RetrieveModel(c *gin.Context, modelType int) {
	modelId := c.Param("model")
	if aiModel, ok := openAIModelsMap[modelId]; ok {
		switch modelType {
		case constant.ChannelTypeAnthropic:
			c.JSON(200, dto.AnthropicModel{
				ID:          aiModel.Id,
				CreatedAt:   time.Unix(int64(aiModel.Created), 0).UTC().Format(time.RFC3339),
				DisplayName: aiModel.Id,
				Type:        "model",
			})
		default:
			c.JSON(200, aiModel)
		}
	} else {
		openAIError := types.OpenAIError{
			Message: fmt.Sprintf("The model '%s' does not exist", modelId),
			Type:    "invalid_request_error",
			Param:   "model",
			Code:    "model_not_found",
		}
		c.JSON(200, gin.H{
			"error": openAIError,
		})
	}
}
