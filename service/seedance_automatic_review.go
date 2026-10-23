package service

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"sync"
)

// Only an explicit rejection without a created video permits a second generation
// submission. Copyright, generic sensitive-content errors and transport failures
// must not trigger material import or a generation retry.
func ShouldAutomaticallyReviewSeedance(channelID, status int, name string, body []byte) bool {
	if channelID != 272 || status != http.StatusBadRequest || !IsSeedanceLibraryModel(name) {
		return false
	}
	var envelope struct {
		ID             string `json:"id"`
		TaskID         string `json:"task_id"`
		UpstreamTaskID string `json:"upstreamTaskId"`
		Data           struct {
			ID     string `json:"id"`
			TaskID string `json:"task_id"`
		} `json:"data"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	return common.Unmarshal(body, &envelope) == nil && envelope.ID == "" && envelope.TaskID == "" && envelope.UpstreamTaskID == "" && envelope.Data.ID == "" && envelope.Data.TaskID == "" && envelope.Error.Code == "InputImageSensitiveContentDetected.PrivacyInformation"
}

// This is intentionally bounded by the caller's request. Persistent per-video
// resource records prevent repeating an asset/group POST after uncertain receipt.
// All material review uses the provider's default policy, never a skip strategy.
func PrepareAutomaticSeedanceReview(c *gin.Context, name, key, taskID string, raw []byte) ([]byte, error) {
	if c.GetInt("id") <= 0 || taskID == "" {
		return nil, seedanceError(400, "Material review requires an authenticated video request", "material_review_failed")
	}
	if err := ValidateSeedanceModelAccess(c, name); err != nil {
		return nil, err
	}
	var body map[string]any
	if err := common.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	content, _ := body["content"].([]any)
	type imageRef struct {
		item   map[string]any
		source string
	}
	var images []imageRef
	for _, value := range content {
		item, _ := value.(map[string]any)
		if item["type"] != "image_url" {
			continue
		}
		image, _ := item["image_url"].(map[string]any)
		source, _ := image["url"].(string)
		if strings.HasPrefix(source, "asset://") {
			continue
		}
		if err := ValidateSeedanceAssetSubmission(&SeedanceAssetSubmission{Model: name, Assets: []SeedanceAssetInput{{URL: source}}}); err != nil {
			return nil, err
		}
		images = append(images, imageRef{item, source})
	}
	if len(images) == 0 || len(images) > 9 {
		return nil, seedanceError(400, "No eligible reference images for automatic material review", "material_review_failed")
	}
	credentials, err := loadBytePlusAssetCredentials(272)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
	defer cancel()
	scope := strconv.Itoa(c.GetInt("id")) + ":" + name + ":" + SeedanceKeyFingerprint(key) + ":" + seedanceControlFingerprint(credentials) + ":" + taskID
	makeResource := func(kind, suffix string) *model.SeedanceResource {
		return &model.SeedanceResource{ID: kind + "_auto_" + SeedanceKeyFingerprint(scope + suffix)[:40], Kind: kind, UserID: c.GetInt("id"), ChannelID: 272, KeyFingerprint: SeedanceKeyFingerprint(key), ProjectName: credentials.ProjectName, CredentialFingerprint: seedanceControlFingerprint(credentials), GroupType: "virtual", Model: name, Status: "Creating", Description: "Automatic review after explicit video submission rejection", ParentID: taskID}
	}
	group := makeResource("group", ":group")
	group.Name = group.ID
	defer func() {
		if c.GetBool("seedance_auto_review_ready") {
			return
		}
		_ = QueueAutomaticSeedanceReviewCleanup(c.GetInt("id"), taskID)
	}()
	if err := createAutomaticReviewResource(ctx, group, "CreateAssetGroup", map[string]any{"Name": group.Name, "GroupType": "AIGC"}); err != nil {
		return nil, err
	}
	for i, image := range images {
		asset := makeResource("asset", fmt.Sprintf(":image:%d:%s", i, image.source))
		asset.GroupID = group.ID
		asset.SourceURL = image.source
		asset.AssetType = "Image"
		asset.Name = asset.ID
		if err := createAutomaticReviewResource(ctx, asset, "CreateAsset", map[string]any{"GroupId": group.UpstreamID, "URL": image.source, "AssetType": "Image", "Name": asset.ID}); err != nil {
			return nil, err
		}
		for {
			if err := ctx.Err(); err != nil {
				return nil, seedanceError(400, "Material review did not finish before the request deadline; no video was submitted", "material_review_timeout")
			}
			envelope, err := bytePlusAssetRequestWithErrorDetails(ctx, asset, "GetAsset", map[string]any{"Id": asset.UpstreamID}, true)
			if err != nil {
				return nil, err
			}
			result := seedanceResult(envelope)
			status := seedanceString(result, "Status", "status")
			switch strings.ToLower(status) {
			case "active":
				asset.Status = "Active"
			case "failed", "rejected":
				asset.Status = "Failed"
				detail, _ := result["Error"].(map[string]any)
				asset.FailReason = sanitizeTaskFailure(seedanceString(detail, "Message", "message"), key, credentials.AccessKeyID, credentials.SecretAccessKey, credentials.SessionToken, image.source, asset.UpstreamID, group.UpstreamID)
				if asset.FailReason == "" {
					asset.FailReason = "Reference image did not pass material review"
				}
				_ = seedanceDB().Save(asset).Error
				code := seedanceString(detail, "Code", "code")
				if code == "" {
					code = "material_review_failed"
				}
				return nil, seedanceError(400, asset.FailReason, code)
			default:
				asset.Status = "Processing"
			}
			if err := seedanceDB().Save(asset).Error; err != nil {
				return nil, err
			}
			if asset.Status == "Active" {
				image.item["image_url"] = map[string]any{"url": "asset://" + asset.UpstreamID}
				break
			}
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, seedanceError(400, "Material review did not finish before the request deadline; no video was submitted", "material_review_timeout")
			case <-timer.C:
			}
		}
	}
	encoded, err := common.Marshal(body)
	if err == nil {
		c.Set("seedance_auto_review_ready", true)
	}
	return encoded, err
}

func createAutomaticReviewResource(ctx context.Context, resource *model.SeedanceResource, action string, payload map[string]any) error {
	var existing model.SeedanceResource
	err := seedanceDB().Where("id = ? AND user_id = ? AND kind = ?", resource.ID, resource.UserID, resource.Kind).First(&existing).Error
	if err == nil {
		if existing.UpstreamID == "" {
			return seedanceError(400, "Material submission could not be confirmed; do not repeat this submission", "material_submission_unknown")
		}
		*resource = existing
		return nil
	}
	if err != gorm.ErrRecordNotFound {
		return err
	}
	// Persist before sending: timeouts and restarts can never repeat this POST.
	if err := seedanceDB().Create(resource).Error; err != nil {
		return err
	}
	envelope, err := bytePlusAssetRequestWithErrorDetails(ctx, resource, action, payload, true)
	if err != nil {
		resource.Status = "submission_unknown"
		if detail, ok := err.(*SeedanceAPIError); ok && detail.Status >= 400 && detail.Status < 500 && detail.Code != "material_submission_unknown" {
			resource.Status = "Failed"
		}
		resource.FailReason = err.Error()
		_ = seedanceDB().Save(resource).Error
		return err
	}
	resource.UpstreamID = seedanceString(seedanceResult(envelope), "Id", "id")
	if resource.UpstreamID == "" {
		resource.Status = "submission_unknown"
		_ = seedanceDB().Save(resource).Error
		return seedanceError(400, "Material submission returned no identifier; no video was submitted", "material_submission_unknown")
	}
	resource.Status = "Processing"
	if resource.Kind == "group" {
		resource.Status = "Active"
	}
	return seedanceDB().Save(resource).Error
}

// Queue only marks resources created by this exact video; the singleton worker
// deletes assets before their group and retries failed deletions after restarts.
func QueueAutomaticSeedanceReviewCleanup(userID int, taskID string) error {
	if err := seedanceDB().Where("user_id = ? AND kind = ? AND channel_id = ? AND parent_id = ? AND description = ? AND status = ? AND upstream_id = ?", userID, "group", 272, taskID, "Automatic review after explicit video submission rejection", "Failed", "").Delete(&model.SeedanceResource{}).Error; err != nil {
		return err
	}
	return seedanceDB().Model(&model.SeedanceResource{}).Where("user_id = ? AND kind = ? AND channel_id = ? AND parent_id = ? AND description = ?", userID, "group", 272, taskID, "Automatic review after explicit video submission rejection").Update("status", "cleanup_pending").Error
}

func MarkAutomaticSeedanceReviewSending(userID int, taskID string, upstreamID string) error {
	data, _ := common.Marshal(map[string]any{"generation_submitted": true, "generation_task_id": upstreamID})
	return seedanceDB().Model(&model.SeedanceResource{}).Where("user_id = ? AND kind = ? AND channel_id = ? AND parent_id = ? AND description = ?", userID, "group", 272, taskID, "Automatic review after explicit video submission rejection").Update("result_data", string(data)).Error
}

var automaticReviewCleanupOnce sync.Once

func startSeedanceAutomaticReviewCleanupWorker() {
	automaticReviewCleanupOnce.Do(func() {
		go func() {
			for {
				ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
				err := CleanupAutomaticSeedanceReviews(ctx)
				cancel()
				if err != nil {
					common.SysLog("Automatic Seedance material cleanup pending; will retry")
				}
				time.Sleep(15 * time.Second)
			}
		}()
	})
}

func CleanupAutomaticSeedanceReviews(ctx context.Context) error {
	var groups []model.SeedanceResource
	if err := seedanceDB().Where("kind = ? AND channel_id = ? AND description = ?", "group", 272, "Automatic review after explicit video submission rejection").Order("created_at").Limit(100).Find(&groups).Error; err != nil {
		return err
	}
	var firstError error
	for i := range groups {
		group := &groups[i]
		ready := group.Status == "cleanup_pending"
		if !ready {
			var task model.Task
			err := seedanceDB().Where("user_id = ? AND task_id = ?", group.UserID, group.ParentID).First(&task).Error
			if err == nil {
				ready = task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure
				if task.Status == model.TaskStatusFailure {
					var meta struct {
						Submitted bool   `json:"generation_submitted"`
						TaskID    string `json:"generation_task_id"`
					}
					_ = common.UnmarshalJsonStr(group.ResultData, &meta)
					if meta.Submitted {
						id := meta.TaskID
						if id == "" {
							id = task.GetUpstreamTaskID()
						}
						ready = false
						if id != "" {
							terminal, e := automaticReviewUpstreamFinished(ctx, group, id)
							if e != nil {
								if firstError == nil {
									firstError = e
								}
								continue
							}
							ready = terminal
						}
					}
				}

			} else if err != gorm.ErrRecordNotFound {
				if firstError == nil {
					firstError = err
				}
				continue
			} else {
				// A persisted provider ID also covers a successful upstream submission
				// followed by a gateway restart before its local task was inserted.
				var meta struct {
					Submitted bool   `json:"generation_submitted"`
					TaskID    string `json:"generation_task_id"`
				}
				_ = common.UnmarshalJsonStr(group.ResultData, &meta)
				if meta.TaskID != "" {
					terminal, err := automaticReviewUpstreamFinished(ctx, group, meta.TaskID)
					if err != nil {
						if firstError == nil {
							firstError = err
						}
						continue
					}
					ready = terminal
				} else if !meta.Submitted && group.CreatedAt < time.Now().Unix()-model.VideoSubmissionLeaseSeconds {
					ready = true
				}
			}
		}
		if !ready {
			continue
		}
		if err := QueueAutomaticSeedanceReviewCleanup(group.UserID, group.ParentID); err != nil {
			if firstError == nil {
				firstError = err
			}
			continue
		}
		var assets []model.SeedanceResource
		if err := seedanceDB().Where("user_id = ? AND group_id = ? AND kind = ? AND parent_id = ?", group.UserID, group.ID, "asset", group.ParentID).Find(&assets).Error; err != nil {
			if firstError == nil {
				firstError = err
			}
			continue
		}
		failed := false
		for j := range assets {
			if err := deleteAutomaticSeedanceResource(ctx, &assets[j], "DeleteAsset"); err != nil {
				failed = true
				if firstError == nil {
					firstError = err
				}
			}
		}
		if failed {
			continue
		}
		if err := deleteAutomaticSeedanceResource(ctx, group, "DeleteAssetGroup"); err != nil && firstError == nil {
			firstError = err
		}
	}
	return firstError
}

func deleteAutomaticSeedanceResource(ctx context.Context, r *model.SeedanceResource, action string) error {
	absent := r.UpstreamID == "" && r.Status == "Failed"
	if r.UpstreamID == "" && !absent {
		found, err := recoverAutomaticSeedanceResource(ctx, r)
		if err != nil {
			return err
		}
		if !found {
			return seedanceError(409, "Material creation is unconfirmed; cleanup will retry", "material_cleanup_pending")
		}
		absent = r.Status == "absent"
	}
	if !absent {
		_, err := bytePlusAssetRequestWithErrorDetails(ctx, r, action, map[string]any{"Id": r.UpstreamID}, true)
		if err != nil {
			detail, ok := err.(*SeedanceAPIError)
			if !ok || detail.Status != http.StatusNotFound {
				return err
			}
		}
	}
	// Remove media URLs with the local resource as well, while retaining a tombstone.
	if err := seedanceDB().Model(r).Updates(map[string]any{"status": "deleted", "source_url": "", "result_data": ""}).Error; err != nil {
		return err
	}
	return seedanceDB().Delete(r).Error
}

func automaticReviewUpstreamFinished(ctx context.Context, r *model.SeedanceResource, taskID string) (bool, error) {
	ch, err := model.GetChannelById(272, true)
	if err != nil {
		return false, err
	}
	if !isVolcSeedanceChannel(ch) {
		return false, fmt.Errorf("original generation route is unavailable")
	}
	key, _, err := SeedanceResourceKey(ch, r.KeyFingerprint)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(ch.GetBaseURL(), "/")+"/api/v3/contents/generations/tasks/"+url.PathEscape(taskID), nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return true, nil
	}
	if resp.StatusCode != 200 {
		return false, fmt.Errorf("unable to confirm video completion")
	}
	var result struct {
		Status string `json:"status"`
	}
	if err := common.DecodeJson(resp.Body, &result); err != nil {
		return false, err
	}
	return result.Status == "succeeded" || result.Status == "failed" || result.Status == "cancelled" || result.Status == "expired", nil
}

// Resolve uncertain control-plane POSTs by their unique persisted name. Never
// resubmit them and never delete another user's pre-existing material.
func recoverAutomaticSeedanceResource(ctx context.Context, r *model.SeedanceResource) (bool, error) {
	action := "ListAssetGroups"
	filter := map[string]any{"GroupType": "AIGC", "Name": r.Name}
	if r.Kind == "asset" {
		action = "ListAssets"
		group, err := model.GetSeedanceResource(r.UserID, "group", r.GroupID)
		if err != nil || group.UpstreamID == "" {
			return false, fmt.Errorf("original material group is unconfirmed")
		}
		filter["GroupIds"] = []string{group.UpstreamID}
	}
	next := ""
	for page := 0; page < 10; page++ {
		fields := map[string]any{"Filter": filter, "MaxResults": 100}
		if next != "" {
			fields["NextToken"] = next
		}
		env, err := bytePlusAssetRequestWithErrorDetails(ctx, r, action, fields, true)
		if err != nil {
			return false, err
		}
		result := seedanceResult(env)
		items, _ := result["Items"].([]any)
		for _, v := range items {
			item, _ := v.(map[string]any)
			if seedanceString(item, "Name") == r.Name && seedanceString(item, "Id") != "" {
				r.UpstreamID = seedanceString(item, "Id")
				return true, seedanceDB().Model(r).Update("upstream_id", r.UpstreamID).Error
			}
		}
		next = seedanceString(result, "NextToken")
		if next == "" {
			if r.CreatedAt < time.Now().Unix()-300 {
				r.Status = "absent"
				return true, nil
			}
			return false, nil
		}
	}
	return false, fmt.Errorf("material recovery pagination is incomplete")
}
