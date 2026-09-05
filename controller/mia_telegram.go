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

// Mia only aliases IDs that are strictly upstream spellings, never a generic
// request fallback. Some ModelIDCandidates are separate public products, such
// as Nano Banana and Nano Banana 2, and must remain independently selectable.
var miaCatalogModelAliases = map[string]string{
	"claude-haiku-4-5-20251001":  "claude-haiku-4-5",
	"anthropic/claude-haiku-4.5": "claude-haiku-4-5",
}

func miaCatalogModelID(raw string) string {
	modelID := strings.TrimSpace(raw)
	if canonical, ok := miaCatalogModelAliases[strings.ToLower(modelID)]; ok {
		return canonical
	}
	return modelID
}

type miaTelegramAPIKeyRequest struct {
	TelegramUserID string `json:"telegram_user_id"`
	Model          string `json:"model"`
}

type miaDebugIdentitiesRequest struct {
	Emails []string `json:"emails"`
}

type miaModelCatalogItem struct {
	ID                     string                  `json:"id"`
	DisplayName            string                  `json:"display_name"`
	Vendor                 string                  `json:"vendor"`
	Capability             string                  `json:"capability"`
	Recommended            bool                    `json:"recommended"`
	SupportsVision         bool                    `json:"supports_vision"`
	VisionRecommended      bool                    `json:"vision_recommended"`
	VideoCapabilities      *miaVideoCapabilities   `json:"video_capabilities,omitempty"`
	Pricing                *miaModelPricing        `json:"pricing,omitempty"`
	SupportedEndpointTypes []constant.EndpointType `json:"supported_endpoint_types"`
}

type miaModelPricing struct {
	Unit          string   `json:"unit"`
	InputPrice    *float64 `json:"input_price,omitempty"`
	OutputPrice   *float64 `json:"output_price,omitempty"`
	Price         *float64 `json:"price,omitempty"`
	Currency      string   `json:"currency"`
	DiscountRatio *float64 `json:"discount_ratio,omitempty"`
	ChannelName   string   `json:"channel_name,omitempty"`
}

type miaIntegerRange struct {
	Min     int `json:"min"`
	Max     int `json:"max"`
	Default int `json:"default"`
}

type miaVideoCapabilities struct {
	Modes              []string        `json:"modes"`
	DurationSeconds    miaIntegerRange `json:"duration_seconds"`
	Resolutions        []string        `json:"resolutions"`
	DefaultResolution  string          `json:"default_resolution"`
	AspectRatios       []string        `json:"aspect_ratios"`
	DefaultAspectRatio string          `json:"default_aspect_ratio"`
	MaxReferenceImages int             `json:"max_reference_images"`
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

// ResolveMiaDebugIdentities maps a small server-controlled email allowlist to
// bound Telegram identities. It is only available to Mia's internal service.
func ResolveMiaDebugIdentities(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var input miaDebugIdentitiesRequest
	if err := common.DecodeJson(c.Request.Body, &input); err != nil || len(input.Emails) == 0 || len(input.Emails) > 20 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "invalid_request", "message": "invalid request"})
		return
	}
	emails := make([]string, 0, len(input.Emails))
	seen := make(map[string]struct{})
	for _, raw := range input.Emails {
		email := strings.ToLower(strings.TrimSpace(raw))
		if email == "" || len(email) > 254 || !strings.Contains(email, "@") {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "code": "invalid_request", "message": "invalid request"})
			return
		}
		if _, exists := seen[email]; !exists {
			seen[email] = struct{}{}
			emails = append(emails, email)
		}
	}
	type debugIdentity struct {
		Email      string `json:"email"`
		TelegramID string `json:"telegram_user_id"`
	}
	rows := make([]debugIdentity, 0, len(emails))
	if err := model.DB.Table("users").
		Select("LOWER(email) AS email, telegram_id").
		Where("LOWER(email) IN ? AND telegram_id <> '' AND status = ?", emails, common.UserStatusEnabled).
		Scan(&rows).Error; err != nil {
		common.SysError("failed to resolve Mia debug identities: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "code": "identity_lookup_failed", "message": "unable to resolve identities"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"identities": rows}})
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
			if strings.EqualFold(miaCatalogModelID(accessibleModel.Id), miaCatalogModelID(modelName)) {
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
	pricingByModel := make(map[string]model.Pricing)
	for _, pricing := range model.GetPricing() {
		pricingByModel[strings.ToLower(pricing.ModelName)] = pricing
	}
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
			canonicalID := miaCatalogModelID(accessibleModel.Id)
			displayName, isCurated := catalogModelTabLabel(canonicalID)
			if !isCurated {
				continue
			}
			endpointTypes := model.GetModelSupportEndpointTypes(canonicalID)
			if len(endpointTypes) == 0 {
				endpointTypes = accessibleModel.SupportedEndpointTypes
			}
			capability, ok := miaModelCapability(endpointTypes)
			if !ok {
				continue
			}
			displayPricing := resolveMiaModelPricing(canonicalID, capability)
			if displayPricing == nil {
				continue
			}
			pricing := pricingByModel[strings.ToLower(canonicalID)]
			supportsVision := capability == "chat" && miaModelHasTag(pricing.Tags, "vision")
			visionRecommended := supportsVision && miaModelHasTag(pricing.Tags, "vision-recommended")
			videoCapabilities := miaVideoModelCapabilities(capability)
			modelItems[strings.ToLower(canonicalID)] = miaModelCatalogItem{
				ID:                     canonicalID,
				DisplayName:            displayName,
				Vendor:                 accessibleModel.OwnedBy,
				Capability:             capability,
				Recommended:            miaModelHasTag(pricing.Tags, "recommended"),
				SupportsVision:         supportsVision,
				VisionRecommended:      visionRecommended,
				VideoCapabilities:      videoCapabilities,
				Pricing:                displayPricing,
				SupportedEndpointTypes: endpointTypes,
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

func resolveMiaModelPricing(modelName, capability string) *miaModelPricing {
	candidates := make([]PublicMarketplaceItem, 0)
	for _, row := range publicMarketplacePricingRows(modelName) {
		item := publicMarketplacePriceItem(modelName, row)
		if strings.TrimSpace(item.ClientExclusive) != "" || item.UserPrice == nil || *item.UserPrice <= 0 {
			continue
		}
		candidates = append(candidates, item)
	}
	if len(candidates) == 0 {
		return nil
	}
	sortPublicMarketplaceItems(candidates)
	lowest := candidates[0]
	pricing := &miaModelPricing{Currency: "USD"}
	switch capability {
	case "chat":
		pricing.Unit = "token_1m"
		pricing.InputPrice = lowest.UserPrice
		if lowest.ActualOutputUserPrice != nil && *lowest.ActualOutputUserPrice > 0 {
			pricing.OutputPrice = lowest.ActualOutputUserPrice
		}
		if lowest.OfficialInputPrice != nil && *lowest.OfficialInputPrice > *lowest.UserPrice {
			pricing.DiscountRatio = float64Ptr(*lowest.UserPrice / *lowest.OfficialInputPrice)
		}
	case "image":
		pricing.Unit = "image"
		pricing.Price = lowest.UserPrice
		if lowest.OfficialInputPrice != nil && *lowest.OfficialInputPrice > *lowest.UserPrice {
			pricing.DiscountRatio = float64Ptr(*lowest.UserPrice / *lowest.OfficialInputPrice)
		}
	case "video":
		pricing.Unit = "second"
		pricing.Price = lowest.UserPrice
		if lowest.OfficialInputPrice != nil && *lowest.OfficialInputPrice > *lowest.UserPrice {
			pricing.DiscountRatio = float64Ptr(*lowest.UserPrice / *lowest.OfficialInputPrice)
		}
	default:
		return nil
	}
	return pricing
}

func float64Ptr(value float64) *float64 { return &value }

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
		case constant.EndpointTypeOpenAI, constant.EndpointTypeOpenAIResponse,
			constant.EndpointTypeOpenAIResponseCompact, constant.EndpointTypeAnthropic,
			constant.EndpointTypeGemini:
			return "chat", true
		}
	}
	return "", false
}

func miaModelHasTag(tags, expected string) bool {
	for _, tag := range strings.FieldsFunc(tags, func(r rune) bool {
		switch r {
		case ',', ';', '|', ' ', '\t', '\r', '\n':
			return true
		default:
			return false
		}
	}) {
		if strings.EqualFold(tag, expected) {
			return true
		}
	}
	return false
}

// miaVideoModelCapabilities supplies only a provider-neutral fallback for
// video drafts. The model category itself always comes from API Master endpoint
// metadata; this fallback is not a model allowlist.
func miaVideoModelCapabilities(capability string) *miaVideoCapabilities {
	if capability != "video" {
		return nil
	}
	return &miaVideoCapabilities{
		Modes:              []string{"text_to_video", "image_to_video"},
		DurationSeconds:    miaIntegerRange{Min: 4, Max: 15, Default: 4},
		Resolutions:        []string{"720p"},
		DefaultResolution:  "720p",
		AspectRatios:       []string{"1:1", "16:9", "9:16"},
		DefaultAspectRatio: "16:9",
		MaxReferenceImages: 10,
	}
}
