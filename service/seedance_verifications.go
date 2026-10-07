package service

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type SeedanceVerificationInput struct {
	Model       string `json:"model"`
	CallbackURL string `json:"callback_url"`
	Name        string `json:"name,omitempty"`
	ChannelID   int    `json:"channel_id,omitempty"`
}

// The provider callback is browser navigation, never an authentication proof.
func CreateSeedanceVerification(c *gin.Context, input SeedanceVerificationInput) (*model.SeedanceResource, error) {
	if input.Model == "" {
		input.Model = "seedance-2.5"
	}
	if input.Model != "seedance-2.0" && input.Model != "seedance-2.0-fast" && input.Model != "seedance-2.0-mini" && input.Model != "seedance-2.5" {
		return nil, seedanceError(400, "Real-person verification requires a verified public model ID")
	}
	if err := ValidateSeedanceModelAccess(c, input.Model); err != nil {
		return nil, err
	}
	if input.CallbackURL != "" {
		return nil, seedanceError(400, "Custom callback_url is not supported; APIMaster hosts the callback")
	}
	if utf8.RuneCountInString(input.Name) > 64 {
		return nil, seedanceError(400, "Group name must not exceed 64 characters")
	}
	key := c.GetHeader("Idempotency-Key")
	if len(key) < 8 || len(key) > 128 {
		return nil, seedanceError(400, "Idempotency-Key of 8–128 characters is required")
	}
	if input.ChannelID > 0 {
		if fixed, ok := common.GetContextKey(c, constant.ContextKeyTokenSpecificChannelId); ok && fixed != strconv.Itoa(input.ChannelID) {
			return nil, seedanceError(403, "Requested channel conflicts with this key")
		}
		common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, strconv.Itoa(input.ChannelID))
	}
	request, _ := common.Marshal(map[string]any{"input": input, "channel": common.GetContextKeyString(c, constant.ContextKeyTokenSpecificChannelId)})
	id := "verification_" + SeedanceKeyFingerprint(key)[:32]
	// Include the authenticated owner; keys chosen by two customers cannot collide.
	id = "verification_" + SeedanceKeyFingerprint(id + ":" + strconv.Itoa(c.GetInt("id")))[:32]
	if existing, err := model.GetSeedanceResource(c.GetInt("id"), "verification", id); err == nil {
		if existing.RequestData != string(request) {
			return nil, seedanceError(409, "Idempotency-Key was used for a different request")
		}
		return existing, nil
	}
	origin := os.Getenv("SEEDANCE_CALLBACK_ORIGIN")
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, seedanceError(503, "Hosted portrait callback is not configured")
	}
	resource, err := selectSeedanceLibraryWithFilter(c, input.Model, isBytePlusSeedanceChannel)
	if err != nil {
		return nil, seedanceError(503, "Selected channel does not support configured official H5 verification; no alternative route was used", "channel_not_supported")
	}
	var recent int64
	if err := seedanceDB().Model(&model.SeedanceResource{}).Where("kind = ? AND user_id = ? AND created_at > ?", "verification", c.GetInt("id"), time.Now().Unix()-60).Count(&recent).Error; err != nil {
		return nil, err
	}
	if recent >= 3 {
		return nil, seedanceError(429, "Too many verification sessions; query an existing session")
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	state := hex.EncodeToString(nonce[:])
	resource.ID, resource.Kind, resource.Status = id, "verification", "creating"
	resource.CallbackHash = SeedanceKeyFingerprint(state)
	resource.ExpiresAt = time.Now().Unix() + 1800
	resource.RequestData = string(request)
	resource.Name = input.Name
	if resource.Name == "" {
		resource.Name = "Verified portrait"
	}
	if err := seedanceDB().Create(resource).Error; err != nil {
		existing, e := model.GetSeedanceResource(c.GetInt("id"), "verification", id)
		if e == nil && existing.RequestData == string(request) {
			return existing, nil
		}
		return nil, err
	}
	callback := strings.TrimRight(origin, "/") + "/v1/seedance2/private-avatar/callback/" + id + "/" + state
	envelope, err := bytePlusAssetRequest(c.Request.Context(), resource, "CreateVisualValidateSession", map[string]any{"CallbackURL": callback})
	if err != nil {
		resource.Status = "submission_unknown"
		if e, ok := err.(*SeedanceAPIError); ok && e.Code == "entitlement_required" {
			resource.Status = "failed"
			resource.FailReason = "entitlement_required"
		}
		_ = seedanceDB().Model(resource).Updates(map[string]any{"status": resource.Status, "fail_reason": resource.FailReason}).Error
		return resource, nil // Durable receipt; never repeat the POST automatically.
	}
	result := seedanceResult(envelope)
	resource.UpstreamID = seedanceString(result, "BytedToken")
	resource.VerificationTokenHash = SeedanceKeyFingerprint(resource.UpstreamID)
	link := seedanceString(result, "H5Link")
	linkURL, linkErr := url.Parse(link)
	if resource.UpstreamID == "" || linkErr != nil || linkURL.Scheme != "https" || linkURL.Hostname() == "" || linkURL.User != nil {
		resource.Status = "submission_unknown"
	} else {
		encoded, _ := common.Marshal(map[string]any{"verification_url": link})
		resource.ResultData, resource.Status = string(encoded), "pending"
	}
	if err := seedanceDB().Save(resource).Error; err != nil {
		return nil, err
	}
	return resource, nil
}

func RefreshSeedanceVerification(c *gin.Context, resource *model.SeedanceResource) error {
	if err := ValidateSeedanceModelAccess(c, resource.Model); err != nil {
		return err
	}
	return confirmSeedanceVerification(c, resource)
}

func confirmSeedanceVerification(c *gin.Context, resource *model.SeedanceResource) error {
	if resource.Status == "completed" || resource.Status == "expired" || resource.Status == "failed" {
		return nil
	}
	if resource.ExpiresAt > 0 && time.Now().Unix() >= resource.ExpiresAt {
		resource.Status, resource.ResultData = "expired", ""
		return seedanceDB().Model(resource).Updates(map[string]any{"status": "expired", "result_data": "", "upstream_id": ""}).Error
	}
	if resource.UpstreamID == "" {
		return nil
	}
	ch, err := model.GetChannelById(resource.ChannelID, true)
	if err != nil || !isBytePlusSeedanceChannel(ch) {
		return seedanceError(503, "Media library is temporarily unavailable")
	}
	if _, _, err := SeedanceResourceKey(ch, resource.KeyFingerprint); err != nil {
		return err
	}
	envelope, err := bytePlusAssetRequest(c.Request.Context(), resource, "GetVisualValidateResult", map[string]any{"BytedToken": resource.UpstreamID})
	if err != nil {
		return err
	} // Temporary errors never become authentication failure.
	upstreamGroup := seedanceString(seedanceResult(envelope), "GroupId")
	if upstreamGroup == "" {
		return nil
	}
	group := model.SeedanceResource{ID: "group_" + strings.TrimPrefix(resource.ID, "verification_"), Kind: "group", UserID: resource.UserID, ChannelID: resource.ChannelID, KeyFingerprint: resource.KeyFingerprint, ProjectName: resource.ProjectName, CredentialFingerprint: resource.CredentialFingerprint, GroupType: "real_person", VerificationID: resource.ID, UpstreamID: upstreamGroup, Model: resource.Model, Name: resource.Name, Status: "Active"}
	return seedanceDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&group).Error; err != nil {
			return err
		}
		resource.GroupID, resource.Status, resource.ResultData = group.ID, "completed", ""
		return tx.Model(resource).Updates(map[string]any{"group_id": group.ID, "status": "completed", "result_data": "", "link_consumed": true, "upstream_id": ""}).Error
	})
}

func HandleSeedanceVerificationCallback(c *gin.Context, id, state, token, resultCode string) error {
	var resource model.SeedanceResource
	if seedanceDB().Where("id = ? AND kind = ?", id, "verification").First(&resource).Error != nil || resource.CallbackHash == "" || subtle.ConstantTimeCompare([]byte(resource.CallbackHash), []byte(SeedanceKeyFingerprint(state))) != 1 || resource.VerificationTokenHash == "" || subtle.ConstantTimeCompare([]byte(resource.VerificationTokenHash), []byte(SeedanceKeyFingerprint(token))) != 1 {
		return seedanceError(400, "Callback could not be associated with a verification session", "callback_mismatch")
	}
	// Consume the link, but do not accept a browser's success/failure claim as proof.
	if err := seedanceDB().Model(&resource).Updates(map[string]any{"link_consumed": true, "result_data": ""}).Error; err != nil {
		return err
	}
	err := confirmSeedanceVerification(c, &resource)
	if err == nil && resource.Status != "completed" && resource.Status != "expired" && resultCode != "" && resultCode != "10000" {
		return seedanceDB().Model(&resource).Update("status", "failed_unconfirmed").Error
	}
	return err
}

func SeedanceVerificationDTO(resource *model.SeedanceResource) map[string]any {
	dto := map[string]any{"id": resource.ID, "session_id": resource.ID, "object": "seedance.avatar.verification", "model": resource.Model, "method": "official_h5", "status": resource.Status, "created_at": resource.CreatedAt, "expires_at": resource.ExpiresAt}
	if resource.Status == "completed" {
		dto["group_id"] = resource.GroupID
	} else if resource.Status == "pending" && !resource.LinkConsumed && (resource.ExpiresAt == 0 || time.Now().Unix() < resource.ExpiresAt) {
		var result map[string]any
		if common.UnmarshalJsonStr(resource.ResultData, &result) == nil {
			dto["verification_url"] = result["verification_url"]
			dto["h5_link"] = result["verification_url"]
		}
	}
	if resource.Status == "submission_unknown" {
		dto["error"] = map[string]any{"code": "submission_unknown", "message": "Upstream acceptance is uncertain; do not create another session automatically", "trace_id": resource.ID}
	}
	if resource.Status == "failed" {
		dto["error"] = map[string]any{"code": resource.FailReason, "message": "Upstream account permission or entitlement is required", "trace_id": resource.ID}
	}
	return dto
}

// Worker housekeeping removes expired H5 credentials even when a user abandons
// the browser and never polls again. Bounded batches keep the existing loop light.
func ExpireSeedanceVerificationSessions() error {
	var ids []string
	if err := seedanceDB().Model(&model.SeedanceResource{}).Where("kind = ? AND expires_at > 0 AND expires_at <= ? AND status IN ?", "verification", time.Now().Unix(), []string{"creating", "pending", "failed_unconfirmed", "submission_unknown"}).Limit(100).Pluck("id", &ids).Error; err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	return seedanceDB().Model(&model.SeedanceResource{}).Where("id IN ? AND status IN ?", ids, []string{"creating", "pending", "failed_unconfirmed", "submission_unknown"}).Updates(map[string]any{"status": "expired", "result_data": "", "upstream_id": ""}).Error
}
