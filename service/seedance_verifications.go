package service

import (
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type SeedanceVerificationInput struct {
	Model       string `json:"model"`
	CallbackURL string `json:"callback_url"`
	Name        string `json:"name,omitempty"`
}

// Authentication is performed by the portrait owner on the provider H5 page.
// The callback is only a navigation target; it is never accepted as proof.
func CreateSeedanceVerification(c *gin.Context, input SeedanceVerificationInput) (*model.SeedanceResource, error) {
	if input.Model == "" {
		input.Model = "seedance-2.5"
	}
	parsed, err := url.Parse(input.CallbackURL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || len(input.CallbackURL) > 8192 {
		return nil, seedanceError(400, "callback_url must be a public HTTPS URL")
	}
	check := SeedanceAssetSubmission{Model: input.Model, URL: &input.CallbackURL}
	if err := ValidateSeedanceAssetSubmission(&check); err != nil {
		return nil, err
	}
	if utf8.RuneCountInString(input.Name) > 64 {
		return nil, seedanceError(400, "Group name must not exceed 64 characters")
	}
	resource, err := selectSeedanceLibraryWithFilter(c, input.Model, isBytePlusSeedanceChannel)
	if err != nil {
		return nil, err
	}
	resource.ID, resource.Kind, resource.Status = seedanceID("verification"), "verification", "pending"
	resource.Name = input.Name
	if resource.Name == "" {
		resource.Name = "Verified portrait"
	}
	if err := model.DB.Create(resource).Error; err != nil {
		return nil, err
	}
	envelope, err := bytePlusAssetRequest(c.Request.Context(), resource, "CreateVisualValidateSession", map[string]any{"CallbackURL": input.CallbackURL})
	if err != nil {
		model.DB.Delete(resource)
		return nil, err
	}
	result := seedanceResult(envelope)
	resource.UpstreamID = seedanceString(result, "BytedToken")
	link := seedanceString(result, "H5Link")
	linkURL, linkErr := url.Parse(link)
	if resource.UpstreamID == "" || linkErr != nil || linkURL.Scheme != "https" || linkURL.Hostname() == "" || linkURL.User != nil {
		model.DB.Delete(resource)
		return nil, seedanceError(502, "Invalid portrait verification response")
	}
	encoded, err := common.Marshal(map[string]any{"verification_url": link})
	if err != nil {
		return nil, err
	}
	resource.ResultData = string(encoded)
	if err := model.DB.Save(resource).Error; err != nil {
		return nil, err
	}
	return resource, nil
}

func RefreshSeedanceVerification(c *gin.Context, resource *model.SeedanceResource) error {
	if err := ValidateSeedanceModelAccess(c, resource.Model); err != nil {
		return err
	}
	if resource.Status == "completed" {
		return nil
	}
	ch, err := model.GetChannelById(resource.ChannelID, true)
	if err != nil || !isBytePlusSeedanceChannel(ch) || !seedanceChannelAllowed(c, ch, resource.Model) {
		return seedanceError(503, "Media library is temporarily unavailable")
	}
	if _, _, err := SeedanceResourceKey(ch, resource.KeyFingerprint); err != nil {
		return err
	}
	envelope, err := bytePlusAssetRequest(c.Request.Context(), resource, "GetVisualValidateResult", map[string]any{"BytedToken": resource.UpstreamID})
	if err != nil {
		return err
	}
	upstreamGroup := seedanceString(seedanceResult(envelope), "GroupId")
	if upstreamGroup == "" {
		return nil
	}
	// A deterministic public ID makes concurrent polls idempotent. Only the
	// provider's authenticated result can create an owned real-portrait group.
	group := model.SeedanceResource{ID: "group_" + strings.TrimPrefix(resource.ID, "verification_"), Kind: "group", UserID: resource.UserID, ChannelID: resource.ChannelID, KeyFingerprint: resource.KeyFingerprint, UpstreamID: upstreamGroup, Model: resource.Model, Name: resource.Name, Status: "Active"}
	return model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&group).Error; err != nil {
			return err
		}
		resource.GroupID, resource.Status = group.ID, "completed"
		return tx.Model(resource).Updates(map[string]any{"group_id": group.ID, "status": "completed"}).Error
	})
}

func SeedanceVerificationDTO(resource *model.SeedanceResource) map[string]any {
	dto := map[string]any{"id": resource.ID, "object": "seedance.avatar.verification", "model": resource.Model, "status": resource.Status, "created_at": resource.CreatedAt}
	if resource.Status == "completed" {
		dto["group_id"] = resource.GroupID
	} else {
		var result map[string]any
		if common.UnmarshalJsonStr(resource.ResultData, &result) == nil {
			dto["verification_url"] = result["verification_url"]
		}
	}
	return dto
}
