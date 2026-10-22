package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const xingchiAutomaticReviewDescription = "Retained automatic review after explicit video rejection"

// Keep billing and task creation in the existing relay: only the provider POST
// is repeated, and only after an explicit, task-less face rejection and Active
// review. Never retry ambiguous transport outcomes or other safety rejections.
func DoXingchiAutomaticReview(c *gin.Context, channelID int, name, key, taskID string, body io.Reader, send func(io.Reader) (*http.Response, error)) (*http.Response, error) {
	if !SupportsRetainedSeedanceAutomaticReview(channelID) || !IsSeedanceLibraryModel(name) {
		return send(body)
	}
	raw, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	resp, err := send(bytes.NewReader(raw))
	if err != nil || resp == nil || resp.StatusCode != http.StatusBadRequest {
		return resp, err
	}
	rejected, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(rejected))
	var envelope struct {
		ID             string `json:"id"`
		TaskID         string `json:"task_id"`
		UpstreamTaskID string `json:"upstreamTaskId"`
		Error          struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if common.Unmarshal(rejected, &envelope) != nil || envelope.ID != "" || envelope.TaskID != "" || envelope.UpstreamTaskID != "" || !retainedSeedancePrivacyRejection(channelID, envelope.Error.Code) || c.GetBool("xingchi_automatic_review_attempted") || (channelID == 274 && beeNexReviewHasTaskID(rejected)) {
		return resp, nil
	}
	c.Set("xingchi_automatic_review_attempted", true)
	reviewed, err := prepareRetainedSeedanceAutomaticReview(c, channelID, name, key, taskID, raw)
	if err != nil {
		// No second video POST has occurred. Leave materials for inspection and do
		// not misclassify this confirmed rejection as an ambiguous paid submission.
		c.Set("xingchi_automatic_review_rejected", true)
		message := xingchiAutomaticReviewFailure(err.Error(), key)
		encoded, marshalErr := common.Marshal(map[string]any{"error": map[string]any{"code": "invalid_asset_material", "message": message, "type": "BadRequest"}})
		if marshalErr != nil {
			return nil, marshalErr
		}
		resp.Body = io.NopCloser(bytes.NewReader(encoded))
		resp.ContentLength = int64(len(encoded))
		return resp, nil
	}
	if err := c.Request.Context().Err(); err != nil {
		return nil, err
	}
	_ = resp.Body.Close()
	// Exactly one reviewed generation attempt; neither this function nor the
	// resource ledger retries a creation POST after an unconfirmed response.
	return send(bytes.NewReader(reviewed))
}

func prepareXingchiAutomaticReview(c *gin.Context, name, key, taskID string, raw []byte) ([]byte, error) {
	return prepareRetainedSeedanceAutomaticReview(c, 292, name, key, taskID, raw)
}

func prepareRetainedSeedanceAutomaticReview(c *gin.Context, channelID int, name, key, taskID string, raw []byte) ([]byte, error) {
	if c.GetInt("id") <= 0 || taskID == "" {
		return nil, seedanceError(400, "Automatic material review requires an authenticated video request", "invalid_asset_material")
	}
	if err := ValidateSeedanceModelAccess(c, name); err != nil {
		return nil, err
	}
	ch, err := model.GetChannelById(channelID, true)
	if err != nil || ch == nil || ch.Status != common.ChannelStatusEnabled {
		return nil, seedanceError(503, "Media library is temporarily unavailable")
	}
	if _, _, err := SeedanceResourceKey(ch, SeedanceKeyFingerprint(key)); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := common.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	// BeeNex keeps reference content inside metadata. Preserve the original JSON
	// representation, including large seeds and explicit false/zero values.
	generationFields := fields
	if channelID == 274 {
		if !isBeeNexAutomaticReviewModel(name) {
			return nil, seedanceError(400, "Unsupported BeeNex model", "invalid_asset_material")
		}
		generationFields = nil
		if err := common.Unmarshal(fields["metadata"], &generationFields); err != nil || generationFields == nil {
			return nil, seedanceError(400, "Invalid BeeNex metadata", "invalid_asset_material")
		}
	}
	var moderation *SeedanceModeration
	if isTokenHubSeedanceChannel(ch) {
		// Automatic TokenHub ingestion defaults to Skip; explicit policies still
		// pass the same validation as client-supplied moderation.
		moderation = &SeedanceModeration{Strategy: "Skip"}
		if value, exists := fields["Moderation"]; exists {
			moderation, err = ParseSeedanceModeration(value)
			if err != nil {
				return nil, err
			}
		}
	}
	var content []map[string]json.RawMessage
	if err := common.Unmarshal(generationFields["content"], &content); err != nil {
		return nil, err
	}
	type reference struct {
		item   map[string]json.RawMessage
		image  map[string]json.RawMessage
		source string
		kind   string
	}
	images := []reference{}
	for _, item := range content {
		var kind string
		_ = common.Unmarshal(item["type"], &kind)
		if kind != "image_url" && !(channelID == 298 && kind == "video_url") {
			continue
		}
		var image map[string]json.RawMessage
		if err := common.Unmarshal(item[kind], &image); err != nil {
			return nil, err
		}
		var source string
		if err := common.Unmarshal(image["url"], &source); err != nil {
			return nil, err
		}
		if strings.HasPrefix(source, "asset://") {
			continue
		}
		if err := ValidateSeedanceAssetSubmission(&SeedanceAssetSubmission{Model: name, Assets: []SeedanceAssetInput{{URL: source}}}); err != nil {
			return nil, err
		}
		images = append(images, reference{item, image, source, kind})
	}
	if len(images) == 0 || len(images) > 9 {
		return nil, seedanceError(400, "No eligible reference images for automatic material review", "invalid_asset_material")
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
	defer cancel()
	var beeNexContext model.SeedanceResource
	if channelID == 274 {
		if err := bindBeeNexAutomaticReviewContext(&beeNexContext); err != nil {
			return nil, err
		}
	}
	scope := strconv.Itoa(c.GetInt("id")) + ":" + name + ":" + SeedanceKeyFingerprint(key) + ":" + taskID
	if channelID != 292 {
		scope = strconv.Itoa(channelID) + ":" + scope
	}
	if channelID == 274 {
		scope += ":" + beeNexContext.CredentialFingerprint
	}
	metadata, err := common.Marshal(map[string]any{"video_task_id": taskID, "automatic_review": true})
	if err != nil {
		return nil, err
	}
	resource := func(kind, suffix string) *model.SeedanceResource {
		return &model.SeedanceResource{ID: kind + "_xcauto_" + SeedanceKeyFingerprint(scope + suffix)[:36], Kind: kind, UserID: c.GetInt("id"), ChannelID: channelID, KeyFingerprint: SeedanceKeyFingerprint(key), ProjectName: "default", GroupType: "virtual", Model: name, Status: "Creating", Description: xingchiAutomaticReviewDescription, RequestData: string(metadata)}
	}
	group := resource("group", ":group")
	group.Name = group.ID
	if channelID == 274 {
		group.ProjectName, group.CredentialFingerprint = beeNexContext.ProjectName, beeNexContext.CredentialFingerprint
	}
	if isTokenHubSeedanceChannel(ch) {
		if err := bindTokenHubAssetContext(group); err != nil {
			return nil, err
		}
	}
	if err := createXingchiAutomaticReviewResource(ctx, group, "CreateAssetGroup", map[string]any{"Name": group.Name, "GroupType": "AIGC"}); err != nil {
		return nil, err
	}
	for i, image := range images {
		asset := resource("asset", fmt.Sprintf(":image:%d:%s", i, image.source))
		asset.GroupID = group.ID
		asset.CredentialFingerprint = group.CredentialFingerprint
		asset.ProjectName = group.ProjectName
		asset.SourceURL = image.source
		asset.AssetType = "Image"
		if image.kind == "video_url" {
			asset.AssetType = "Video"
		}
		asset.Name = asset.ID
		payload := map[string]any{"GroupId": group.UpstreamID, "URL": image.source, "AssetType": asset.AssetType, "Name": asset.ID}
		if moderation != nil {
			payload["Moderation"] = moderation
			requested, err := common.Marshal(map[string]any{"video_task_id": taskID, "automatic_review": true, "moderation": moderation, "moderation_forwarded": false})
			if err != nil {
				return nil, err
			}
			asset.RequestData = string(requested)
		}
		if err := createXingchiAutomaticReviewResource(ctx, asset, "CreateAsset", payload); err != nil {
			return nil, err
		}
		for {
			if ctx.Err() != nil {
				return nil, seedanceError(400, "Material review did not finish before the request deadline; no video was submitted. Materials are retained in your library.", "material_review_timeout")
			}
			envelope, err := xingchiAutomaticReviewAction(ctx, asset, "GetAsset", map[string]any{"Id": asset.UpstreamID})
			if err != nil {
				return nil, err
			}
			result := seedanceResult(envelope)
			switch strings.ToLower(seedanceString(result, "Status", "status")) {
			case "active":
				asset.Status = "Active"
			case "failed", "rejected":
				asset.Status = "Failed"
				detail, _ := result["Error"].(map[string]any)
				if detail == nil {
					detail, _ = result["error"].(map[string]any)
				}
				message := seedanceString(detail, "Message", "message")
				if message == "" {
					message = seedanceString(result, "FailReason", "fail_reason", "Message", "message")
				}
				sensitive := []string{key, image.source, asset.UpstreamID, group.UpstreamID}
				if isTokenHubSeedanceChannel(ch) {
					if creds, e := loadTokenHubAssetCredentials(channelID); e == nil {
						sensitive = append(sensitive, creds.AccessKeyID, creds.SecretAccessKey)
					}
				}
				if channelID == 274 {
					sensitive = append(sensitive, beeNexAutomaticReviewSecrets()...)
				}
				asset.FailReason = xingchiAutomaticReviewFailure(message, sensitive...)
				if asset.FailReason == "" || asset.FailReason == "Task failed" {
					asset.FailReason = "Reference image did not pass material review"
				}
				if err := seedanceDB().Save(asset).Error; err != nil {
					return nil, err
				}
				return nil, seedanceError(400, fmt.Sprintf("Material review failed for image %d: %s", i+1, asset.FailReason), "invalid_asset_material")
			default:
				asset.Status = "Processing"
			}
			if err := seedanceDB().Save(asset).Error; err != nil {
				return nil, err
			}
			if asset.Status == "Active" {
				image.image["url"], err = common.Marshal("asset://" + asset.UpstreamID)
				if err != nil {
					return nil, err
				}
				image.item[image.kind], err = common.Marshal(image.image)
				if err != nil {
					return nil, err
				}
				break
			}
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, seedanceError(400, "Material review did not finish before the request deadline; no video was submitted. Materials are retained in your library.", "material_review_timeout")
			case <-timer.C:
			}
		}
	}
	// Every explicit false/zero, media role, text, video/audio reference and all
	// other generation fields survive unchanged. Only image URL values change.
	generationFields["content"], err = common.Marshal(content)
	if err != nil {
		return nil, err
	}
	if channelID == 274 {
		fields["metadata"], err = common.Marshal(generationFields)
		if err != nil {
			return nil, err
		}
	}
	return common.Marshal(fields)
}

func createXingchiAutomaticReviewResource(ctx context.Context, r *model.SeedanceResource, action string, fields map[string]any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var existing model.SeedanceResource
	err := seedanceDB().Where("id = ? AND user_id = ? AND kind = ?", r.ID, r.UserID, r.Kind).First(&existing).Error
	if err == nil {
		if existing.UpstreamID == "" {
			return seedanceError(400, "Material submission could not be confirmed; do not repeat this submission", "material_submission_unknown")
		}
		*r = existing
		return nil
	}
	if err != gorm.ErrRecordNotFound {
		return err
	}
	// A durable claim before POST prevents duplicate creation on races/restarts.
	if err := seedanceDB().Create(r).Error; err != nil {
		return err
	}
	envelope, err := xingchiAutomaticReviewAction(ctx, r, action, fields)
	if err != nil {
		r.Status = "submission_unknown"
		if e, ok := err.(*SeedanceAPIError); ok && e.Status >= 400 && e.Status < 500 && e.Code != "submission_unknown" {
			r.Status = "Failed"
		}
		r.FailReason = err.Error()
		_ = seedanceDB().Save(r).Error
		return err
	}
	r.UpstreamID = seedanceString(seedanceResult(envelope), "Id", "id")
	if r.UpstreamID == "" {
		r.Status = "submission_unknown"
		_ = seedanceDB().Save(r).Error
		return seedanceError(400, "Material submission returned no identifier; no video was submitted", "material_submission_unknown")
	}
	// As with manual ingestion, record forwarding only after a confirmed
	// CreateAsset response. Reused resources retain their original evidence.
	if action == "CreateAsset" && fields["Moderation"] != nil {
		var metadata map[string]any
		if err := common.UnmarshalJsonStr(r.RequestData, &metadata); err != nil {
			return err
		}
		metadata["moderation_forwarded"] = true
		encoded, err := common.Marshal(metadata)
		if err != nil {
			return err
		}
		r.RequestData = string(encoded)
	}
	r.Status = "Processing"
	if r.Kind == "group" {
		r.Status = "Active"
	}
	return seedanceDB().Save(r).Error
}

func xingchiAutomaticReviewFailure(reason string, sensitive ...string) string {
	return sanitizeTaskFailure(reason, sensitive...)
}
func xingchiAutomaticReviewAction(ctx context.Context, r *model.SeedanceResource, action string, fields map[string]any) (map[string]any, error) {
	return seedanceActionRequestWithReviewDetails(ctx, r, action, fields, true)
}

// Only explicit privacy rejections without an upstream task permit a reviewed
// retry. Other moderation decisions and unknown transport outcomes are final.
func retainedSeedancePrivacyRejection(channelID int, code string) bool {
	return code == "InputImageSensitiveContentDetected.PrivacyInformation" || (channelID == 298 && code == "InputVideoSensitiveContentDetected.PrivacyInformation")
}
