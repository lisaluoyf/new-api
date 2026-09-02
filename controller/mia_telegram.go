package controller

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const miaDefaultChatModel = "grok-4.5"

type miaTelegramAPIKeyRequest struct {
	TelegramUserID string `json:"telegram_user_id"`
	Model          string `json:"model"`
}

type miaModelCatalogItem struct {
	ID                     string                  `json:"id"`
	DisplayName            string                  `json:"display_name"`
	Vendor                 string                  `json:"vendor"`
	Capability             string                  `json:"capability"`
	Recommended            bool                    `json:"recommended"`
	SupportedEndpointTypes []constant.EndpointType `json:"supported_endpoint_types"`
}

func resolveMiaTelegramUser(c *gin.Context) (miaTelegramAPIKeyRequest, *model.User, bool) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var input miaTelegramAPIKeyRequest
	if err := common.DecodeJson(c.Request.Body, &input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"code":    "invalid_request",
			"message": "invalid request",
		})
		return input, nil, false
	}

	telegramUserID := strings.TrimSpace(input.TelegramUserID)
	parsedTelegramID, err := strconv.ParseInt(telegramUserID, 10, 64)
	if err != nil || parsedTelegramID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"code":    "invalid_telegram_user_id",
			"message": "invalid Telegram user id",
		})
		return input, nil, false
	}

	user := &model.User{TelegramId: telegramUserID}
	if err := user.FillUserByTelegramId(); err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"code":    "telegram_not_bound",
			"message": "Telegram account is not bound",
		})
		return input, nil, false
	}
	if user.Status != common.UserStatusEnabled {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"code":    "user_disabled",
			"message": "user is disabled",
		})
		return input, nil, false
	}
	return input, user, true
}

// ResolveMiaTelegramAPIKey resolves an existing APIMaster user's usable token
// for the Mia bot. The route is protected by Mia's dedicated service secret.
func ResolveMiaTelegramAPIKey(c *gin.Context) {
	input, user, ok := resolveMiaTelegramUser(c)
	if !ok {
		return
	}

	modelName := strings.TrimSpace(input.Model)
	if modelName == "" {
		modelName = miaDefaultChatModel
	}
	token, err := getMiaUsableTokenForModel(user.Id, modelName)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			common.SysError("failed to resolve Mia user token: " + err.Error())
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"code":    "token_lookup_failed",
				"message": "unable to resolve API key",
			})
			return
		}
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"code":    "no_usable_api_key",
			"message": "no usable API key",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"user_id":  user.Id,
			"token_id": token.Id,
			"api_key":  token.GetFullKey(),
		},
	})
}

func getMiaUsableTokenForModel(userID int, modelName string) (*model.Token, error) {
	tokens, err := model.GetUsableUserTokensForTrustedService(userID)
	if err != nil {
		return nil, err
	}
	model.GetPricing()
	for i := range tokens {
		accessibleModels, accessErr := GetAccessibleOpenAIModelsForToken(userID, &tokens[i])
		if accessErr != nil {
			return nil, accessErr
		}
		for _, accessibleModel := range accessibleModels {
			if accessibleModel.Id == modelName {
				return &tokens[i], nil
			}
		}
	}
	return nil, gorm.ErrRecordNotFound
}

// GetMiaTelegramModelCatalog returns the union of models reachable through at
// least one Mia-usable token. Token identifiers and keys never leave this API.
func GetMiaTelegramModelCatalog(c *gin.Context) {
	_, user, ok := resolveMiaTelegramUser(c)
	if !ok {
		return
	}

	tokens, err := model.GetUsableUserTokensForTrustedService(user.Id)
	if err != nil {
		common.SysError("failed to resolve Mia model catalog tokens: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"code":    "token_lookup_failed",
			"message": "unable to resolve model catalog",
		})
		return
	}
	if len(tokens) == 0 {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"code":    "no_usable_api_key",
			"message": "no usable API key",
		})
		return
	}

	// Populate the endpoint metadata cache from enabled abilities before model
	// classification. This is the same source used by GET /v1/models.
	model.GetPricing()
	modelItems := make(map[string]miaModelCatalogItem)
	for i := range tokens {
		accessibleModels, accessErr := GetAccessibleOpenAIModelsForToken(user.Id, &tokens[i])
		if accessErr != nil {
			common.SysError("failed to resolve Mia accessible models: " + accessErr.Error())
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"code":    "model_lookup_failed",
				"message": "unable to resolve model catalog",
			})
			return
		}
		for _, accessibleModel := range accessibleModels {
			capability, ok := miaModelCapability(accessibleModel.SupportedEndpointTypes)
			if !ok {
				continue
			}
			modelItems[accessibleModel.Id] = miaModelCatalogItem{
				ID:                     accessibleModel.Id,
				DisplayName:            accessibleModel.Id,
				Vendor:                 accessibleModel.OwnedBy,
				Capability:             capability,
				Recommended:            miaRecommendedModel(accessibleModel.Id, capability),
				SupportedEndpointTypes: accessibleModel.SupportedEndpointTypes,
			}
		}
	}

	items := make([]miaModelCatalogItem, 0, len(modelItems))
	for _, item := range modelItems {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Capability != items[j].Capability {
			return items[i].Capability < items[j].Capability
		}
		return items[i].ID < items[j].ID
	})

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"user_id": user.Id,
			"models":  items,
		},
	})
}

func miaModelCapability(endpointTypes []constant.EndpointType) (string, bool) {
	for _, endpointType := range endpointTypes {
		if endpointType == constant.EndpointTypeOpenAIVideo {
			return "video", true
		}
	}
	for _, endpointType := range endpointTypes {
		if endpointType == constant.EndpointTypeImageGeneration {
			return "image", true
		}
	}
	for _, endpointType := range endpointTypes {
		switch endpointType {
		case constant.EndpointTypeOpenAI:
			return "chat", true
		}
	}
	return "", false
}

func miaRecommendedModel(modelID, capability string) bool {
	switch capability {
	case "chat":
		return modelID == miaDefaultChatModel
	case "image":
		return modelID == "gpt-image-2"
	case "video":
		return strings.EqualFold(modelID, "minimax-h3")
	default:
		return false
	}
}
