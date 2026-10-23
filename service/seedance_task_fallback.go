package service

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Injected by relay to keep the service/relay dependency one-way. The returned
// closure only sends/parses the request; it never reserves or settles money.
var PrepareSeedanceFallbackSubmit func(*gin.Context, *model.Task, *model.Channel, string) (func() (*SeedanceFallbackResult, error), error)

type SeedanceFallbackResult struct {
	UpstreamID    string
	Platform      constant.TaskPlatform
	UpstreamModel string
	Data          []byte
}

func CaptureSeedanceFallbackRequest(c *gin.Context, task *model.Task) {
	if !IsSeedanceLibraryModel(task.Properties.OriginModelName) || task.Action == constant.TaskActionRemix || strings.Contains(c.Request.URL.Path, "/remix") || !strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
		return
	}
	if c.GetBool("seedance_user_pinned_channel") {
		return
	}
	if c.GetBool("seedance_unified_explicit_channel") {
		return
	}
	fields, err := SeedanceRequestFields(c)
	if err != nil || len(fields) == 0 || fields["draft_task_id"] != nil || fields["draft"] == true {
		return
	}
	// A stable, deep copy prevents later protocol normalization from replacing
	// logical asset IDs with the original provider's private IDs.
	raw, err := common.Marshal(fields)
	if err != nil {
		return
	}
	var copyFields map[string]any
	if common.Unmarshal(raw, &copyFields) != nil {
		return
	}
	task.PrivateData.SeedanceFallback = &model.SeedanceFallbackRequest{Fields: copyFields, TokenGroup: common.GetContextKeyString(c, constant.ContextKeyTokenGroup), UserGroup: common.GetContextKeyString(c, constant.ContextKeyUserGroup), ClientType: common.GetContextKeyString(c, constant.ContextKeyClientType), Policy: ProviderPolicyForModel(c, task.Properties.OriginModelName)}
}

// Narrow allowlist: a terminal submission/service failure is retryable, but
// parameter, moderation, copyright, transport ambiguity and timeout are not.
func seedanceAsyncFailureRetryable(reason string) bool {
	r := strings.ToLower(strings.TrimSpace(reason))
	r = strings.TrimSuffix(r, ".")
	// Exact terminal diagnoses only. In particular, never treat a policy or
	// moderation explanation containing "banned" as an account failure.
	switch r {
	case "user has been banned", "account has been banned", "account has been disabled",
		"invalid api key", "api key has expired", "api key has been revoked",
		"service temporarily unavailable":
		return true
	}
	for _, blocked := range []string{"copyright", "sensitive", "moderation", "safety", "policy", "invalid", "pixel", "aspect", "width", "height", "timeout", "timed out", "1 hour", "审核", "版权"} {
		if strings.Contains(r, blocked) {
			return false
		}
	}
	return r == "task submission failed; please check the request and try again later" || r == "task submission failed" || r == "internal server error" || r == "service unavailable"
}

func seedanceFallbackContext(ctx context.Context, task *model.Task) (*gin.Context, error) {
	snapshot := task.PrivateData.SeedanceFallback
	if snapshot == nil {
		return nil, fmt.Errorf("Missing fallback request")
	}
	raw, err := common.Marshal(snapshot.Fields)
	if err != nil {
		return nil, err
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request, _ = http.NewRequestWithContext(ctx, http.MethodPost, "/v1/videos/generations", bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", task.UserId)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, task.Properties.OriginModelName)
	common.SetContextKey(c, constant.ContextKeyTokenGroup, snapshot.TokenGroup)
	common.SetContextKey(c, constant.ContextKeyUserGroup, snapshot.UserGroup)
	common.SetContextKey(c, constant.ContextKeyClientType, snapshot.ClientType)
	if snapshot.Policy != nil {
		common.SetContextKey(c, constant.ContextKeyUserSetting, dto.UserSetting{ProviderSettings: []dto.ProviderSetting{*snapshot.Policy}})
	}
	return c, nil
}

func seedanceFallbackAssets(c *gin.Context, fields map[string]any) ([]*model.SeedanceResource, error) {
	assets := []*model.SeedanceResource{}
	seen := map[string]bool{}
	_, err := visitSeedanceAssetURLs(fields, func(id string) (string, error) {
		if seen[id] {
			return "asset://" + id, nil
		}
		seen[id] = true
		a, e := seedanceOwnedAsset(c, id)
		if e != nil {
			return "", e
		}
		if a.SourceURL == "" && a.GroupType != unifiedSeedanceKind {
			return "", fmt.Errorf("Original material is unavailable for review")
		}
		assets = append(assets, a)
		return "asset://" + id, nil
	})
	return assets, err
}

func chooseSeedanceFallbackChannel(c *gin.Context, task *model.Task) (*model.Channel, error) {
	name := task.Properties.OriginModelName
	assets, err := seedanceFallbackAssets(c, task.PrivateData.SeedanceFallback.Fields)
	if err != nil {
		return nil, err
	}
	filter := ChannelPickFilter(c, name)
	return selectUnifiedPricedChannel(name, []int{task.ChannelId}, func(ch *model.Channel) bool {
		if !seedanceChannelAllowed(c, ch, name) || (filter != nil && !filter(ch)) {
			return false
		}
		for _, a := range assets {
			if ch.Id == 272 {
				if _, e := seedance272OriginalImage(c, a.ID); e != nil {
					return false
				}
				continue
			}
			if !unifiedSeedanceLibraryEligible(c, ch, name) {
				return false
			}
			// H5 identity verification is account-bound and cannot be fabricated by
			// background recovery. Only an existing approved target copy is usable.
			if IsSeedancePortraitAsset(a) && ch.Id != constant.VideoFeeSeedanceChannelID {
				return false
			}
		}
		return true
	}, ProviderPolicyForModel(c, name))
}

// Called only for an upstream terminal FAILURE, before refund or terminal event.
func beginSeedanceTaskFallback(ctx context.Context, task *model.Task, from model.TaskStatus, reason string) (bool, error) {
	if task != nil && task.PrivateData.ProviderObservationOnly {
		return false, nil
	}
	if task == nil || from == model.TaskStatusSuccess || from == model.TaskStatusFailure || from == model.TaskStatusRetrying {
		return false, nil
	}
	snapshot := task.PrivateData.SeedanceFallback
	if PrepareSeedanceFallbackSubmit == nil || snapshot == nil || snapshot.Started || !seedanceAsyncFailureRetryable(reason) {
		return false, nil
	}
	c, err := seedanceFallbackContext(ctx, task)
	if err != nil {
		return false, nil
	}
	defer common.CleanupBodyStorage(c)
	ch, err := chooseSeedanceFallbackChannel(c, task)
	if err != nil {
		return false, nil
	}
	cost, priceErr := ChannelProcurementPricesResolved(ch.Id, task.Properties.OriginModelName)
	if priceErr != nil || cost == nil || cost.InputPrice <= 0 {
		return false, nil
	}
	variant, _ := task.PrivateData.SeedanceRequest["billing_variant"].(string)
	variantRatio := ratio_setting.GetVideoModelOfficialPriceRatio(task.Properties.OriginModelName, variant)
	if variantRatio <= 0 {
		return false, nil
	}
	now := time.Now().Unix()
	job := model.SeedanceTaskFallback{TaskID: task.ID, State: "preparing", TargetChannelID: ch.Id, TargetUnitCost: cost.InputPrice * variantRatio, OriginalChannelID: task.ChannelId, OriginalUpstreamID: task.GetUpstreamTaskID(), OriginalReason: reason, Deadline: now + 1800, CreatedAt: now, UpdatedAt: now}
	snapshot.Started = true
	snapshot.Stage = "preparing_materials"
	task.Status = model.TaskStatusRetrying
	task.Progress = "0%"
	task.FailReason = ""
	task.FinishTime = 0
	won := false
	err = seedanceDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&job).Error; err != nil {
			return err
		}
		res := tx.Model(&model.Task{}).Where("id = ? AND status = ? AND channel_id = ?", task.ID, from, job.OriginalChannelID).Updates(map[string]any{"status": task.Status, "progress": task.Progress, "fail_reason": "", "finish_time": 0, "private_data": task.PrivateData, "data": task.Data})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return model.ErrVideoSubmissionClaimLost
		}
		won = true
		return nil
	})
	if err == nil && won {
		logger.LogInfo(ctx, fmt.Sprintf("Seedance async fallback scheduled: task=%s primary_channel=%d backup_channel=%d reason=%s", task.TaskID, job.OriginalChannelID, job.TargetChannelID, reason))
	}
	return won, err
}

// Lease protects material operations across pollers; the sending state is a
// permanent boundary rather than a lease, so uncertain POSTs cannot be replayed.
func processSeedanceTaskFallbacks(ctx context.Context) {
	if PrepareSeedanceFallbackSubmit == nil {
		return
	}
	var jobs []model.SeedanceTaskFallback
	if err := seedanceDB().Where("state IN ? AND lease_until < ?", []string{"preparing", "submitting", "failed", "accepted"}, time.Now().Unix()).Order("lease_until, task_id").Limit(20).Find(&jobs).Error; err != nil {
		logger.LogError(ctx, "Read video recovery jobs failed")
		return
	}
	for i := range jobs {
		if ctx.Err() != nil {
			break
		}
		job := &jobs[i]
		now := time.Now().Unix()
		token := common.GetUUID()
		res := seedanceDB().Model(job).Where("state = ? AND lease_until < ?", job.State, now).Updates(map[string]any{"lease_token": token, "lease_until": now + 180, "updated_at": now})
		if res.Error != nil || res.RowsAffected != 1 {
			continue
		}
		job.LeaseToken = token
		if err := processSeedanceTaskFallback(ctx, job); err != nil {
			logger.LogError(ctx, fmt.Sprintf("Video recovery task %d: %v", job.TaskID, err))
		}
		_ = seedanceDB().Model(job).Where("lease_token = ?", token).Update("lease_until", time.Now().Unix()+15).Error
	}
}

func processSeedanceTaskFallback(ctx context.Context, job *model.SeedanceTaskFallback) error {
	var task model.Task
	if err := seedanceDB().First(&task, job.TaskID).Error; err != nil {
		return err
	}
	if task.Status != model.TaskStatusRetrying {
		if task.Status == model.TaskStatusFailure {
			if err := refundSeedanceFallbackTask(&task, task.FailReason); err != nil {
				return err
			}
		} else if task.Status != model.TaskStatusSuccess {
			return nil
		} else {
			if err := backfillSeedanceFallbackAccounting(&task); err != nil {
				return err
			}
		}
		return seedanceDB().Model(job).Update("state", "done").Error
	}
	if job.State == "failed" {
		return finishSeedanceFallbackFailure(ctx, &task, job, "Backup channel submission failed")
	}
	if job.State == "submitting" {
		// No ID was durably received. Never resend an ambiguous paid submission.
		if time.Now().Unix() < job.Deadline {
			return nil
		}
		return finishSeedanceFallbackFailure(ctx, &task, job, "Backup channel submission could not be confirmed; no further submission was made")
	}
	if time.Now().Unix() >= job.Deadline {
		return finishSeedanceFallbackFailure(ctx, &task, job, "Backup channel material review timed out")
	}
	opctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	c, err := seedanceFallbackContext(opctx, &task)
	if err != nil {
		return finishSeedanceFallbackFailure(ctx, &task, job, "Backup request is unavailable")
	}
	defer common.CleanupBodyStorage(c)
	ch, err := model.GetChannelById(job.TargetChannelID, true)
	if err != nil || ch == nil || !seedanceChannelAllowed(c, ch, task.Properties.OriginModelName) || !ProviderChannelAllowed(ProviderPolicyForModel(c, task.Properties.OriginModelName), ch.Id) || ValidateSeedanceResolutionChannel(c, ch, task.Properties.OriginModelName) != nil {
		return finishSeedanceFallbackFailure(ctx, &task, job, "Backup channel is unavailable for this request")
	}
	ready, key, err := prepareSeedanceFallbackMaterials(c, &task, job, ch)
	if err != nil {
		return finishSeedanceFallbackFailure(ctx, &task, job, err.Error())
	}
	if !ready {
		task.PrivateData.SeedanceFallback.Stage = "reviewing_materials"
		return seedanceDB().Model(&model.Task{}).Where("id = ? AND status = ?", task.ID, model.TaskStatusRetrying).Update("private_data", task.PrivateData).Error
	}
	send, err := PrepareSeedanceFallbackSubmit(c, &task, ch, key)
	if err != nil {
		return finishSeedanceFallbackFailure(ctx, &task, job, "Backup channel does not support this request")
	}
	task.PrivateData.SeedanceFallback.Stage = "submitting_backup"
	// Persist state and exact credential fingerprint atomically with the task.
	err = seedanceDB().Transaction(func(tx *gorm.DB) error {
		r := tx.Model(job).Where("state = ? AND lease_token = ? AND lease_until > ?", "preparing", job.LeaseToken, time.Now().Unix()).Updates(map[string]any{"state": "submitting", "key_fingerprint": SeedanceKeyFingerprint(key), "lease_until": time.Now().Unix() + 180, "updated_at": time.Now().Unix()})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return model.ErrVideoSubmissionClaimLost
		}
		r = tx.Model(&model.Task{}).Where("id = ? AND status = ?", task.ID, model.TaskStatusRetrying).Update("private_data", task.PrivateData)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return model.ErrVideoSubmissionClaimLost
		}
		return nil
	})
	if err != nil {
		return err
	}
	job.State = "submitting"
	result, err := send()
	if err != nil || result == nil || result.UpstreamID == "" {
		// Keep the one-way boundary even for transport/protocol ambiguity.
		_ = seedanceDB().Model(job).Where("lease_token = ?", job.LeaseToken).Update("state", "failed").Error
		return finishSeedanceFallbackFailure(ctx, &task, job, "Backup channel submission failed or could not be confirmed; no further submission was made")
	}
	task.ChannelId = ch.Id
	task.Platform = result.Platform
	task.Properties.UpstreamModelName = result.UpstreamModel
	task.PrivateData.UpstreamTaskID = result.UpstreamID
	task.PrivateData.Key = key
	task.PrivateData.SeedanceFallback.Stage = "generating_backup"
	task.SubmitTime = time.Now().Unix()
	task.Status = model.TaskStatusSubmitted
	task.Progress = "0%"
	task.StartTime = 0
	task.FinishTime = 0
	task.Data = result.Data
	return seedanceDB().Transaction(func(tx *gorm.DB) error {
		// Save the received ID even if a concurrent timeout has terminated the task.
		r := tx.Model(job).Where("state = ? AND lease_token = ?", "submitting", job.LeaseToken).Updates(map[string]any{"state": "accepted", "upstream_id": result.UpstreamID, "updated_at": time.Now().Unix()})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return model.ErrVideoSubmissionClaimLost
		}
		r = tx.Model(&model.Task{}).Where("id = ? AND status = ?", task.ID, model.TaskStatusRetrying).Updates(map[string]any{"status": task.Status, "channel_id": task.ChannelId, "platform": task.Platform, "properties": task.Properties, "private_data": task.PrivateData, "progress": "0%", "start_time": 0, "finish_time": 0, "submit_time": task.SubmitTime, "data": task.Data})
		return r.Error
	})
}

func finishSeedanceFallbackFailure(ctx context.Context, task *model.Task, job *model.SeedanceTaskFallback, reason string) error {
	task.Status = model.TaskStatusFailure
	task.Progress = "100%"
	task.FailReason = reason
	task.FinishTime = time.Now().Unix()
	task.PrivateData.SeedanceFallback.Stage = "failed"
	won, err := task.UpdateWithStatus(model.TaskStatusRetrying)
	if err != nil {
		return err
	}
	if won {
		if err := refundSeedanceFallbackTask(task, reason); err != nil {
			return err
		}
		BackfillTaskLogOnComplete(ctx, task, &relaycommon.TaskInfo{Status: string(model.TaskStatusFailure), Reason: reason})
	}
	return seedanceDB().Model(job).Where("lease_token = ?", job.LeaseToken).Update("state", "done").Error
}

// Every reference must pass review on the selected account. Missing copies are
// created once with durable idempotency keys, then polled on later iterations.
func prepareSeedanceFallbackMaterials(c *gin.Context, task *model.Task, job *model.SeedanceTaskFallback, ch *model.Channel) (bool, string, error) {
	fields := task.PrivateData.SeedanceFallback.Fields
	assets, err := seedanceFallbackAssets(c, fields)
	if err != nil {
		return false, "", fmt.Errorf("Original material is unavailable for backup review")
	}
	if ch.Id == 272 && len(assets) > 0 {
		for _, source := range assets {
			if _, e := seedance272OriginalImage(c, source.ID); e != nil {
				return false, "", e
			}
		}
		key, _, e := ch.GetNextEnabledKey()
		return e == nil, key, e
	}
	if len(assets) == 0 {
		key, _, e := ch.GetNextEnabledKey()
		if e != nil {
			return false, "", fmt.Errorf("Backup channel credential is unavailable")
		}
		return true, key, nil
	}
	key := ""
	ready := true
	replacements := map[string]string{}
	for _, source := range assets {
		logical := source
		// Legacy public assets get a private logical handle for this recovery. The
		// user's original asset and original provider approval remain untouched.
		if source.GroupType != unifiedSeedanceKind {
			logicalID := "fallback_asset_" + SeedanceKeyFingerprint(strconv.Itoa(task.UserId) + ":" + source.ID)[:32]
			logical = &model.SeedanceResource{ID: logicalID, Kind: "asset", UserID: task.UserId, GroupType: unifiedSeedanceKind, GroupID: "fallback_group_" + SeedanceKeyFingerprint(source.GroupID)[:32], SourceURL: source.SourceURL, AssetType: source.AssetType, Status: "Active", Model: task.Properties.OriginModelName, Name: source.Name}
			if err := seedanceDB().Where("id = ?", logicalID).FirstOrCreate(logical).Error; err != nil {
				return false, "", err
			}
			replacements[source.ID] = logical.ID
		}
		var copies []model.SeedanceResource
		if err := seedanceDB().Where("user_id = ? AND parent_id = ? AND kind = ? AND channel_id = ? AND model = ?", task.UserId, logical.ID, "asset", ch.Id, task.Properties.OriginModelName).Order("id").Find(&copies).Error; err != nil {
			return false, "", err
		}
		if len(copies) == 0 {
			if logical.SourceURL == "" {
				return false, "", fmt.Errorf("Original material is unavailable for backup review")
			}
			g := &model.SeedanceResource{ID: logical.GroupID, Kind: "group", UserID: task.UserId, GroupType: unifiedSeedanceKind, Name: "Recovery materials", Status: "Active"}
			if err := seedanceDB().Where("id = ?", g.ID).FirstOrCreate(g).Error; err != nil {
				return false, "", err
			}
			physical, e := unifiedPhysicalGroup(c, g, ch.Id, task.Properties.OriginModelName)
			if e != nil {
				return false, "", fmt.Errorf("Backup material library could not be prepared")
			}
			childKey := "fallback:" + logical.ID + ":" + strconv.Itoa(ch.Id) + ":" + task.Properties.OriginModelName
			review, e := submitSeedanceAssetsSingle(replicaContext(c, ch.Id, childKey), SeedanceAssetSubmission{Model: task.Properties.OriginModelName, GroupID: physical.ID, AssetType: logical.AssetType, Assets: []SeedanceAssetInput{{URL: logical.SourceURL, Name: logical.Name}}, ReplicaParentID: "fallback:" + task.TaskID, LogicalAssetIDs: []string{logical.ID}})
			if e != nil {
				return false, "", fmt.Errorf("Backup material review submission failed")
			}
			if e = PollSeedanceAssetTask(c, review); e != nil {
				return false, "", nil
			}
			if e = seedanceDB().Where("user_id = ? AND parent_id = ? AND kind = ? AND channel_id = ? AND model = ?", task.UserId, logical.ID, "asset", ch.Id, task.Properties.OriginModelName).Find(&copies).Error; e != nil {
				return false, "", e
			}
		} else {
			pendingIDs := map[string]bool{}
			for i := range copies {
				if copies[i].Status != "Active" && copies[i].Status != "Failed" {
					pendingIDs[copies[i].ID] = true
				}
			}
			if len(pendingIDs) > 0 {
				var reviews []model.SeedanceResource
				if e := seedanceDB().Where("user_id = ? AND kind = ? AND channel_id = ? AND model = ? AND status NOT IN ?", task.UserId, "task", ch.Id, task.Properties.OriginModelName, []string{"completed", "failed"}).Find(&reviews).Error; e != nil {
					return false, "", e
				}
				for i := range reviews {
					var reviewIDs []string
					if common.UnmarshalJsonStr(reviews[i].RequestData, &reviewIDs) != nil {
						continue
					}
					matches := false
					for _, id := range reviewIDs {
						if pendingIDs[id] {
							matches = true
						}
					}
					if matches {
						if e := PollSeedanceAssetTask(c, &reviews[i]); e != nil {
							return false, "", nil
						}
					}
				}
				if e := seedanceDB().Where("user_id = ? AND parent_id = ? AND kind = ? AND channel_id = ? AND model = ?", task.UserId, logical.ID, "asset", ch.Id, task.Properties.OriginModelName).Find(&copies).Error; e != nil {
					return false, "", e
				}
			}
		}
		var active *model.SeedanceResource
		pending := false
		for i := range copies {
			if copies[i].Status == "Active" && copies[i].UpstreamID != "" {
				active = &copies[i]
				break
			}
			if copies[i].Status != "Failed" {
				pending = true
			}
		}
		if active == nil {
			if !pending {
				return false, "", fmt.Errorf("Backup channel material review was rejected")
			}
			ready = false
			continue
		}
		k, _, e := SeedanceResourceKey(ch, active.KeyFingerprint)
		if e != nil {
			return false, "", fmt.Errorf("Backup material account is unavailable")
		}
		if key != "" && key != k {
			return false, "", fmt.Errorf("Backup materials belong to different accounts")
		}
		key = k
	}
	if !ready {
		return false, "", nil
	}
	// Recreate the context after replacements so adaptor normalization reads the
	// logical IDs and resolves only the reviewed target copies.
	value, err := visitSeedanceAssetURLs(fields, func(id string) (string, error) {
		if v := replacements[id]; v != "" {
			id = v
		}
		return "asset://" + id, nil
	})
	if err != nil {
		return false, "", err
	}
	raw, err := common.Marshal(value)
	if err != nil {
		return false, "", err
	}
	common.CleanupBodyStorage(c)
	delete(c.Keys, common.KeyRequestBody)
	c.Request.Body = http.NoBody
	c.Request, _ = http.NewRequestWithContext(c.Request.Context(), http.MethodPost, "/v1/videos/generations", bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	// Pin normalization to exactly the copies reviewed above.
	c.Set("seedance_unified_routing", true)
	common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, strconv.Itoa(ch.Id))
	return true, key, nil
}

// Reuse the gateway's durable asynchronous refund ledger: funding and counters
// commit together, and its outbox recovers log delivery without double refunds.
func refundSeedanceFallbackTask(task *model.Task, reason string) error {
	if task.Quota == 0 {
		CompleteSeedanceBillingReceipt(task, true, nil)
		return nil
	}
	row, err := model.FindConsumeLogRowForTask(task.UserId, task.TaskID)
	if err != nil {
		return err
	}
	source := task.PrivateData.BillingSource
	if source == "" {
		source = BillingSourceWallet
	}
	other := taskBillingOther(task)
	other["task_id"] = task.TaskID
	other["reason"] = reason
	other["refund_source"] = "seedance_async_fallback_failure"
	refund := &model.Log{UserId: task.UserId, Username: row.Username, CreatedAt: time.Now().Unix(), Type: model.LogTypeRefund, TokenId: row.TokenId, TokenName: row.TokenName, ModelName: row.ModelName, Quota: row.Quota, ChannelId: row.ChannelId, Group: row.Group, Other: common.MapToJsonStr(other)}
	receipt, err := model.FreezeImageTaskRefund(task, row, refund, source, task.PrivateData.SubscriptionId)
	if err != nil {
		return err
	}
	if _, err = model.ApplyImageTaskRefund(receipt.ID); err != nil {
		return err
	}
	CompleteSeedanceBillingReceipt(task, true, nil)
	return nil
}

// The user's frozen tariff stays unchanged; procurement follows the channel
// that actually generated the output. Original attempt IDs stay in the job.
func backfillSeedanceFallbackAccounting(task *model.Task) error {
	var job model.SeedanceTaskFallback
	if err := seedanceDB().First(&job, task.ID).Error; err != nil {
		return err
	}
	extra := map[string]any{"fallback_triggered": true, "primary_channel_id": job.OriginalChannelID, "fallback_channel_id": job.TargetChannelID}
	if err := model.UpdateLogResultByTaskID(task.UserId, task.TaskID, 0, extra); err != nil {
		return err
	}
	if task.Status != model.TaskStatusSuccess {
		return nil
	}
	row, err := model.FindConsumeLogRowForTask(task.UserId, task.TaskID)
	if err != nil {
		return err
	}
	var snapshot map[string]any
	if err := common.UnmarshalJsonStr(row.AccountingSnapshot, &snapshot); err != nil {
		return err
	}
	prices, ok := snapshot["prices"].(map[string]any)
	if !ok {
		return fmt.Errorf("Missing frozen procurement snapshot")
	}
	tuple, ok := prices["channel_cost"].(map[string]any)
	if !ok {
		return fmt.Errorf("Missing frozen procurement prices")
	}
	tuple["input_price"] = job.TargetUnitCost
	snapshot["channel_id"] = job.TargetChannelID
	snapshot["cost_price_source"] = "configured_video_variant_estimate"
	raw, err := common.Marshal(snapshot)
	if err != nil {
		return err
	}
	return model.LOG_DB.Model(&model.Log{}).Where("id = ?", row.Id).Updates(map[string]any{"channel_id": job.TargetChannelID, "accounting_snapshot": string(raw)}).Error
}

var seedanceFallbackWorkerOnce sync.Once

func startSeedanceTaskFallbackWorker() {
	if !common.IsMasterNode {
		return
	}
	seedanceFallbackWorkerOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				processSeedanceTaskFallbacks(ctx)
				cancel()
			}
		}()
	})
}
