package controller

import (
	"net/http"
	"sort"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

const kimiRegistryDefaultContextSize = 131072

type kimiRegistryModelLimit struct {
	Context int `json:"context"`
}

type kimiRegistryModalities struct {
	Input  []string `json:"input"`
	Output []string `json:"output"`
}

type kimiRegistryModel struct {
	ID         string                 `json:"id"`
	Name       string                 `json:"name"`
	Limit      kimiRegistryModelLimit `json:"limit"`
	ToolCall   bool                   `json:"tool_call"`
	Reasoning  bool                   `json:"reasoning"`
	Modalities kimiRegistryModalities `json:"modalities"`
}

type kimiRegistryProvider struct {
	ID     string                       `json:"id"`
	Name   string                       `json:"name"`
	API    string                       `json:"api"`
	Type   string                       `json:"type"`
	Env    []string                     `json:"env"`
	Models map[string]kimiRegistryModel `json:"models"`
}

func isKimiCodingModel(modelName string, endpoints []constant.EndpointType, pricing model.Pricing) bool {
	hasOpenAIChat := false
	hasNonTextEndpoint := false
	for _, endpoint := range endpoints {
		switch endpoint {
		case constant.EndpointTypeOpenAI:
			hasOpenAIChat = true
		case constant.EndpointTypeImageGeneration,
			constant.EndpointTypeEmbeddings,
			constant.EndpointTypeJinaRerank,
			constant.EndpointTypeOpenAIVideo:
			hasNonTextEndpoint = true
		}
	}

	// Fixed-price models in this deployment are media/task models. Requiring
	// token billing plus the chat-completions endpoint keeps image/video/rerank
	// entries out of a coding-agent registry without maintaining model names.
	return modelName != "" && hasOpenAIChat && !hasNonTextEndpoint && pricing.QuotaType == 0
}

func buildKimiRegistryModels(c *gin.Context) (map[string]kimiRegistryModel, error) {
	pricingByName := make(map[string]model.Pricing)
	for _, item := range model.GetPricing() {
		pricingByName[item.ModelName] = item
	}

	accessible, err := GetAccessibleOpenAIModels(c)
	if err != nil {
		return nil, err
	}

	modelNames := make([]string, 0, len(accessible))
	for _, item := range accessible {
		pricing, ok := pricingByName[item.Id]
		if !ok || !isKimiCodingModel(item.Id, pricing.SupportedEndpointTypes, pricing) {
			continue
		}
		modelNames = append(modelNames, item.Id)
	}
	sort.Strings(modelNames)

	registryModels := make(map[string]kimiRegistryModel, len(modelNames))
	for _, modelName := range modelNames {
		registryModels[modelName] = kimiRegistryModel{
			ID:        modelName,
			Name:      modelName,
			Limit:     kimiRegistryModelLimit{Context: kimiRegistryDefaultContextSize},
			ToolCall:  true,
			Reasoning: true,
			Modalities: kimiRegistryModalities{
				Input:  []string{"text"},
				Output: []string{"text"},
			},
		}
	}
	return registryModels, nil
}

// KimiProviderRegistry returns the models.dev-style api.json schema consumed
// by Kimi Code's "Import custom provider registry" flow.
func KimiProviderRegistry(c *gin.Context) {
	models, err := buildKimiRegistryModels(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	if err := model.RecordRegistryAccess(
		c.GetInt("id"),
		c.GetInt("token_id"),
		len(models),
		c.GetHeader("User-Agent"),
	); err != nil {
		common.SysLog("failed to record Kimi registry access: " + err.Error())
	}

	c.Header("Cache-Control", "private, max-age=300")
	c.Header("Vary", "Authorization, User-Agent")
	c.JSON(http.StatusOK, gin.H{
		"apimaster": kimiRegistryProvider{
			ID:     "apimaster",
			Name:   "APIMaster.ai",
			API:    "https://apimaster.ai/v1",
			Type:   "openai",
			Env:    []string{"APIMASTER_API_KEY"},
			Models: models,
		},
	})
}

func GetRegistryAccessLogs(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	logs, total, err := model.GetRegistryAccessLogs(pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
}
