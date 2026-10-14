package service

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const unifiedSeedanceKind = "unified_virtual"

var unifiedSeedanceModels = []string{"seedance-2.0", "seedance-2.5", "seedance-2.0-fast", "seedance-2.0-mini"}

type unifiedSeedancePlan struct {
	Models map[string][]int `json:"models"`
}

// These managed virtual libraries deliberately include configured official
// accounts, without changing the legacy auto_route_enabled or H5 policies.
func unifiedSeedanceLibraryEligible(c *gin.Context, ch *model.Channel, name string) bool {
	if ch == nil || !seedanceLibraryChannelAllowed(c, ch, name) || !ProviderChannelAllowed(ProviderPolicyForModel(c, name), ch.Id) {
		return false
	}
	if isBytePlusSeedanceChannel(ch) {
		_, err := loadBytePlusAssetCredentials(ch.Id)
		return err == nil
	}
	if isBeeNexSeedanceChannel(ch) && IsBeeNexSeedanceModel(name) {
		_, err := loadBeeNexAssetCredentials(ch.Id)
		return err == nil
	}
	u, err := url.Parse(ch.GetBaseURL())
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return ch.Id == constant.VideoFeeSeedanceChannelID || isXingchiyunSeedanceChannel(ch) || host == "apimart.ai" || strings.HasSuffix(host, ".apimart.ai") || host == "apib.ai" || strings.HasSuffix(host, ".apib.ai")
}
func unifiedSeedanceCurrentPlan(c *gin.Context) (unifiedSeedancePlan, error) {
	plan := unifiedSeedancePlan{Models: map[string][]int{}}
	primaries := map[string]int{}
	pool := map[int]bool{}
	for _, name := range unifiedSeedanceModels {
		if ValidateSeedanceModelAccess(c, name) != nil {
			continue
		}
		ch, err := selectUnifiedPricedChannel(name, nil, func(ch *model.Channel) bool { return unifiedSeedanceLibraryEligible(c, ch, name) }, ProviderPolicyForModel(c, name))
		if err != nil {
			continue
		}
		primaries[name] = ch.Id
		pool[ch.Id] = true
	}
	if len(pool) == 0 {
		return plan, seedanceError(503, "No configured virtual material library is available")
	}
	for name, primary := range primaries {
		plan.Models[name] = []int{primary}
		ids := []int{}
		for id := range pool {
			if id != primary {
				ids = append(ids, id)
			}
		}
		sort.Ints(ids)
		for _, id := range ids {
			ch, err := model.GetChannelById(id, true)
			if err == nil && unifiedSeedanceLibraryEligible(c, ch, name) {
				plan.Models[name] = append(plan.Models[name], id)
			}
		}
	}
	return plan, nil
}
func createUnifiedSeedanceGroup(c *gin.Context, name string, input SeedanceGroupInput) (*model.SeedanceResource, error) {
	if err := ValidateSeedanceModelAccess(c, name); err != nil {
		return nil, err
	}
	if len([]rune(input.Name)) > 255 || len([]rune(input.Description)) > 4000 {
		return nil, seedanceError(400, "Group name or description is too long")
	}
	plan, err := unifiedSeedanceCurrentPlan(c)
	if err != nil {
		return nil, err
	}
	raw, err := common.Marshal(plan)
	if err != nil {
		return nil, err
	}
	g := &model.SeedanceResource{ID: seedanceID("group"), Kind: "group", UserID: c.GetInt("id"), Model: name, GroupType: unifiedSeedanceKind, Name: input.Name, Description: input.Description, Status: "Active", RequestData: string(raw)}
	if g.Name == "" {
		g.Name = "Virtual materials"
	}
	return g, seedanceDB().Create(g).Error
}
func replicaContext(c *gin.Context, channel int, key string) *gin.Context {
	next := c.Copy()
	next.Request = c.Request.Clone(c.Request.Context())
	next.Request.Header = c.Request.Header.Clone()
	next.Request.Header.Set("Idempotency-Key", key)
	next.Set("seedance_replica_internal", true)
	common.SetContextKey(next, constant.ContextKeyTokenSpecificChannelId, strconv.Itoa(channel))
	return next
}
func unifiedPhysicalGroup(c *gin.Context, g *model.SeedanceResource, ch int, name string) (*model.SeedanceResource, error) {
	id := "replica_group_" + SeedanceKeyFingerprint(g.ID + ":" + strconv.Itoa(ch))[:32]
	var slot model.SeedanceResource
	if err := seedanceDB().Where("id = ? AND user_id = ?", id, g.UserID).First(&slot).Error; err == nil {
		if slot.UpstreamID == "" {
			return nil, seedanceError(409, "Material library setup is awaiting confirmation; do not resubmit")
		}
		return &slot, nil
	} else if err != gorm.ErrRecordNotFound {
		return nil, err
	}
	// Claim before remote POST. A crashed or ambiguous owner is never retried.
	slot = model.SeedanceResource{ID: id, Kind: "group", ParentID: g.ID, UserID: g.UserID, GroupType: "virtual", ChannelID: ch, Model: name, Status: "Creating"}
	if err := seedanceDB().Create(&slot).Error; err != nil {
		return nil, err
	}
	physical, err := CreateSeedanceGroup(replicaContext(c, ch, id), name, SeedanceGroupInput{ChannelID: ch, Name: g.Name})
	if err != nil {
		return nil, err
	}
	// The legacy creator makes a durable mapping; retain it as an internal row.
	if err := seedanceDB().Model(physical).Update("parent_id", g.ID).Error; err != nil {
		return nil, err
	}
	slot = *physical
	slot.ID = id
	slot.ParentID = g.ID
	if err := seedanceDB().Save(&slot).Error; err != nil {
		return nil, err
	}
	// One durable public-side handle per account, rather than a second group ID.
	if err := seedanceDB().Delete(physical).Error; err != nil {
		return nil, err
	}
	return &slot, nil
}
func submitUnifiedSeedanceAssets(c *gin.Context, input SeedanceAssetSubmission, g *model.SeedanceResource) (*model.SeedanceResource, error) {
	if !ValidVideoSubmissionKey(c.GetHeader("Idempotency-Key")) {
		return nil, seedanceError(400, "Idempotency-Key of 8–128 visible ASCII characters is required")
	}
	if err := ValidateSeedanceAssetSubmission(&input); err != nil {
		return nil, err
	}
	if input.AssetType != "Image" {
		return nil, seedanceError(400, "Unified virtual materials currently support Image assets")
	}
	if err := ValidateSeedanceModelAccess(c, input.Model); err != nil {
		return nil, err
	}
	raw, err := common.Marshal(input)
	if err != nil {
		return nil, err
	}
	hash := SeedanceKeyFingerprint(string(raw))
	if input.SubmissionHash != "" {
		hash = input.SubmissionHash
	}
	id := "asset_task_" + SeedanceKeyFingerprint(strconv.Itoa(g.UserID) + ":" + c.GetHeader("Idempotency-Key"))[:32]
	if task, err := model.GetSeedanceResource(g.UserID, "task", id); err == nil {
		if task.Description != hash {
			return nil, seedanceError(409, "Idempotency-Key was used for a different request")
		}
		return task, nil
	} else if err != gorm.ErrRecordNotFound {
		return nil, err
	}
	plan, err := unifiedSeedanceCurrentPlan(c)
	if err != nil {
		return nil, err
	}
	pr, err := common.Marshal(plan)
	if err != nil {
		return nil, err
	}
	task := &model.SeedanceResource{ID: id, Kind: "task", UserID: g.UserID, GroupID: g.ID, GroupType: unifiedSeedanceKind, Status: "processing", Model: input.Model, Description: hash, ResultData: string(pr)}
	assets := []model.SeedanceResource{}
	ids := []string{}
	for _, item := range input.Assets {
		a := model.SeedanceResource{ID: seedanceID("asset"), Kind: "asset", UserID: g.UserID, GroupID: g.ID, GroupType: unifiedSeedanceKind, Model: input.Model, AssetType: input.AssetType, Name: item.Name, SourceURL: item.URL, Status: "Pending", RequestData: string(pr), ResultData: task.ID}
		assets = append(assets, a)
		ids = append(ids, a.ID)
	}
	encoded, err := common.Marshal(ids)
	if err != nil {
		return nil, err
	}
	task.RequestData = string(encoded)
	err = seedanceDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(task).Error; err != nil {
			return err
		}
		return tx.Create(&assets).Error
	})
	if err != nil {
		existing, e := model.GetSeedanceResource(g.UserID, "task", id)
		if e == nil && existing.Description == hash {
			return existing, nil
		}
		return nil, err
	}
	names := []string{}
	for name := range plan.Models {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, ch := range plan.Models[name] {
			group, e := unifiedPhysicalGroup(c, g, ch, name)
			if e != nil {
				for _, logical := range assets {
					failed := model.SeedanceResource{ID: "replica_failed_" + SeedanceKeyFingerprint(logical.ID + ":" + name + ":" + strconv.Itoa(ch))[:32], Kind: "asset", ParentID: logical.ID, UserID: g.UserID, GroupID: g.ID, GroupType: "virtual", ChannelID: ch, Model: name, AssetType: input.AssetType, Status: "Failed", FailReason: "Material library could not be prepared"}
					_ = seedanceDB().Create(&failed).Error
				}
				continue
			}
			childKey := id + ":" + strconv.Itoa(ch) + ":" + name
			_, _ = submitSeedanceAssetsSingle(replicaContext(c, ch, childKey), SeedanceAssetSubmission{Model: name, GroupID: group.ID, AssetType: input.AssetType, Assets: input.Assets, ReplicaParentID: task.ID, LogicalAssetIDs: ids})
		}
	}
	// Polling is request-bound. No goroutine outlives the authenticated request.
	_ = pollUnifiedSeedanceTask(c, task)
	return task, nil
}
func refreshUnifiedSeedanceAsset(c *gin.Context, a *model.SeedanceResource) error {
	if err := ensureUnifiedSeedanceCopies(c, a); err != nil {
		return err
	}
	var tasks []model.SeedanceResource
	if err := seedanceDB().Where("user_id = ? AND kind = ? AND group_type = ? AND group_id = ?", a.UserID, "task", unifiedSeedanceKind, a.GroupID).Find(&tasks).Error; err != nil {
		return err
	}
	for i := range tasks {
		if err := pollUnifiedSeedanceTask(c, &tasks[i]); err != nil {
			return err
		}
	}
	fresh, err := model.GetSeedanceResource(a.UserID, "asset", a.ID)
	if err == nil {
		*a = *fresh
	}
	return err
}

// Reconcile newly preferred accounts on explicit readiness queries. Submissions
// have deterministic durable keys; existing/unknown copies are never reposted.
func ensureUnifiedSeedanceCopies(c *gin.Context, a *model.SeedanceResource) error {
	if a.ResultData == "" {
		return nil
	}
	plan, err := unifiedSeedanceCurrentPlan(c)
	if err != nil {
		return nil
	}
	var previous unifiedSeedancePlan
	if err := common.UnmarshalJsonStr(a.RequestData, &previous); err != nil {
		return err
	}
	group, err := model.GetSeedanceResource(a.UserID, "group", a.GroupID)
	if err != nil {
		return err
	}
	changed := false
	for _, name := range unifiedSeedanceModels {
		for _, ch := range plan.Models[name] {
			if !containsUnifiedChannel(previous.Models[name], ch) {
				previous.Models[name] = append(previous.Models[name], ch)
				changed = true
			}
			var n int64
			if err := seedanceDB().Model(&model.SeedanceResource{}).Where("user_id = ? AND parent_id = ? AND model = ? AND channel_id = ?", a.UserID, a.ID, name, ch).Count(&n).Error; err != nil {
				return err
			}
			if n > 0 {
				continue
			}
			physical, e := unifiedPhysicalGroup(c, group, ch, name)
			if e != nil {
				continue
			}
			key := a.ID + ":" + strconv.Itoa(ch) + ":" + name
			_, _ = submitSeedanceAssetsSingle(replicaContext(c, ch, key), SeedanceAssetSubmission{Model: name, GroupID: physical.ID, AssetType: a.AssetType, Assets: []SeedanceAssetInput{{URL: a.SourceURL, Name: a.Name}}, ReplicaParentID: a.ResultData, LogicalAssetIDs: []string{a.ID}})
		}
	}
	if changed {
		raw, e := common.Marshal(previous)
		if e != nil {
			return e
		}
		a.RequestData = string(raw)
		return seedanceDB().Model(a).Update("request_data", a.RequestData).Error
	}
	return nil
}
func containsUnifiedChannel(ids []int, id int) bool {
	for _, n := range ids {
		if n == id {
			return true
		}
	}
	return false
}

func pollUnifiedSeedanceTask(c *gin.Context, task *model.SeedanceResource) error {
	var children []model.SeedanceResource
	if err := seedanceDB().Where("user_id = ? AND kind = ? AND parent_id = ?", task.UserID, "task", task.ID).Find(&children).Error; err != nil {
		return err
	}
	for i := range children {
		if children[i].Status != "completed" && children[i].Status != "failed" {
			_ = PollSeedanceAssetTask(c, &children[i])
		}
	}
	var ids []string
	if err := common.UnmarshalJsonStr(task.RequestData, &ids); err != nil {
		return err
	}
	out := []any{}
	pending := false
	anyReady := false
	for _, id := range ids {
		a, err := model.GetSeedanceResource(task.UserID, "asset", id)
		if err != nil {
			continue
		}
		dto := unifiedSeedanceAssetDTO(a)
		ready, _ := dto["available_models"].([]string)
		all, _ := dto["review_complete"].(bool)
		status := "Pending"
		if len(ready) > 0 {
			anyReady = true
			status = "Active"
		} else if all {
			status = "Failed"
		}
		if !all {
			pending = true
		}
		// Review completion and model readiness are independent; partial approvals
		// remain usable without presenting failed models as ready.
		if err := seedanceDB().Model(a).Update("status", status).Error; err != nil {
			return err
		}
		a.Status = status
		out = append(out, unifiedSeedanceAssetDTO(a))
	}
	task.Status = "completed"
	if !pending && !anyReady {
		task.Status = "failed"
		task.FailReason = "No material copies passed review"
	}
	task.Progress = 100
	if pending {
		task.Status = "processing"
		task.Progress = 50
	}
	raw, err := common.Marshal(map[string]any{"assets": out})
	if err != nil {
		return err
	}
	task.ResultData = string(raw)
	return seedanceDB().Model(task).Updates(map[string]any{"status": task.Status, "progress": task.Progress, "result_data": task.ResultData}).Error
}
func unifiedSeedanceAssetDTO(a *model.SeedanceResource) map[string]any {
	var copies []model.SeedanceResource
	err := seedanceDB().Where("user_id = ? AND kind = ? AND parent_id = ?", a.UserID, "asset", a.ID).Find(&copies).Error
	var plan unifiedSeedancePlan
	_ = common.UnmarshalJsonStr(a.RequestData, &plan)
	available := []string{}
	states := map[string]string{}
	complete := err == nil
	for name, targets := range plan.Models {
		states[name] = "preparing"
		ready := false
		terminal := 0
		seen := 0
		for _, copy := range copies {
			if copy.Model != name || !containsUnifiedChannel(targets, copy.ChannelID) {
				continue
			}
			seen++
			if copy.Status == "Active" && copy.UpstreamID != "" {
				ready = true
				terminal++
			} else if copy.Status == "Failed" {
				terminal++
			}
		}
		if seen < len(targets) || terminal < seen {
			complete = false
		}
		if ready {
			states[name] = "ready"
		} else if seen >= len(targets) && terminal == seen {
			states[name] = "review_failed"
		}
	}
	for name, state := range states {
		if state == "ready" {
			available = append(available, name)
		}
	}
	sort.Strings(available)
	status := "Pending"
	if len(available) > 0 {
		status = "Active"
	} else if complete {
		status = "Failed"
	}
	return map[string]any{"id": a.ID, "asset_id": a.ID, "asset_url": "asset://" + a.ID, "name": a.Name, "asset_type": a.AssetType, "group_id": a.GroupID, "status": status, "available_models": available, "model_readiness": states, "review_complete": complete, "all_models_ready": len(states) > 0 && len(available) == len(states), "created_at": a.CreatedAt, "updated_at": a.UpdatedAt}
}
func updateUnifiedSeedanceResource(c *gin.Context, a *model.SeedanceResource, name, description *string) error {
	if name == nil && description == nil {
		return seedanceError(400, "No editable fields supplied")
	}
	updates := map[string]any{}
	if name != nil {
		if len([]rune(*name)) > 255 {
			return seedanceError(400, "name is too long")
		}
		updates["name"] = *name
	}
	if description != nil && a.Kind == "group" {
		if len([]rune(*description)) > 4000 {
			return seedanceError(400, "description is too long")
		}
		updates["description"] = *description
	}
	return seedanceDB().Model(a).Updates(updates).Error
}
func deleteUnifiedSeedanceResource(c *gin.Context, a *model.SeedanceResource) error {
	if a.Kind == "group" {
		var n int64
		if err := seedanceDB().Model(&model.SeedanceResource{}).Where("user_id = ? AND kind = ? AND group_id = ? AND (parent_id = ? OR parent_id IS NULL)", a.UserID, "asset", a.ID, "").Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return seedanceError(409, "Delete this group's assets before deleting the group")
		}
	}
	var children []model.SeedanceResource
	if err := seedanceDB().Where("user_id = ? AND parent_id = ? AND kind = ?", a.UserID, a.ID, a.Kind).Find(&children).Error; err != nil {
		return err
	}
	for i := range children {
		if err := DeleteSeedanceResource(c, &children[i]); err != nil {
			return err
		}
	}
	return seedanceDB().Delete(a).Error
}

// Resolve a logical asset only after choosing a compatible account. Each model
// has a separately reviewed binding; an approval is never copied across providers.
func unifiedSeedanceCopy(c *gin.Context, id, name string, channel int) (*model.SeedanceResource, error) {
	a, err := model.GetSeedanceResource(c.GetInt("id"), "asset", id)
	if err != nil {
		return nil, seedanceError(400, "Referenced asset was not found in your media library")
	}
	if a.GroupType != unifiedSeedanceKind {
		return a, nil
	}
	var copies []model.SeedanceResource
	if err := seedanceDB().Where("user_id = ? AND parent_id = ? AND kind = ? AND model = ? AND channel_id = ? AND status = ?", a.UserID, a.ID, "asset", name, channel, "Active").Order("id ASC").Find(&copies).Error; err != nil {
		return nil, err
	}
	for i := range copies {
		if copies[i].UpstreamID != "" {
			return &copies[i], nil
		}
	}
	return nil, seedanceError(409, "Material is not ready for the selected model; query its readiness before generating", "asset_not_ready")
}
func chooseUnifiedSeedanceRoute(c *gin.Context, fields map[string]any, banned []int) (*model.SeedanceResource, error) {
	name, _ := fields["model"].(string)
	ids := []string{}
	var legacy *model.SeedanceResource
	hasUnified := false
	_, err := visitSeedanceAssetURLs(fields, func(id string) (string, error) {
		a, e := model.GetSeedanceResource(c.GetInt("id"), "asset", id)
		if e != nil {
			return "", seedanceError(400, "Referenced asset was not found in your media library")
		}
		if a.ParentID != "" {
			return "", seedanceError(400, "Use the public material ID")
		}
		if a.GroupType == unifiedSeedanceKind {
			hasUnified = true
		} else {
			legacy = a
		}
		ids = append(ids, id)
		return "asset://" + id, nil
	})
	if err != nil || !hasUnified {
		return nil, err
	}
	if len(ids) > 20 {
		return nil, seedanceError(400, "Too many material references")
	}
	filter := ChannelPickFilter(c, name)
	eligible := func(ch *model.Channel) bool {
		if !unifiedSeedanceLibraryEligible(c, ch, name) || (filter != nil && !filter(ch)) {
			return false
		}
		if cap, ok := c.Get("seedance_unified_price_cap"); ok {
			base, _ := ratio_setting.GetVideoModelBasePrice(name)
			price, e := ChannelBaseUserPriceResolved(ch.Id, name, base)
			if e != nil || price > cap.(float64)+1e-12 {
				return false
			}
		}
		if legacy != nil && legacy.ChannelID != ch.Id {
			return false
		}
		var account *model.SeedanceResource
		for _, id := range ids {
			a, e := unifiedSeedanceCopy(c, id, name, ch.Id)
			if e != nil || a.Status != "Active" || a.UpstreamID == "" {
				return false
			}
			if a.GroupType == "channel_portrait" && a.Model != name {
				return false
			}
			if account != nil && (account.KeyFingerprint != a.KeyFingerprint || account.ProjectName != a.ProjectName || account.CredentialFingerprint != a.CredentialFingerprint) {
				return false
			}
			account = a
			if isBytePlusSeedanceChannel(ch) {
				credentials, e := loadBytePlusAssetCredentials(ch.Id)
				if e != nil || credentials.ProjectName != a.ProjectName || seedanceControlFingerprint(credentials) != a.CredentialFingerprint {
					return false
				}
			}
			if isBeeNexSeedanceChannel(ch) && IsBeeNexSeedanceModel(name) {
				credentials, e := loadBeeNexAssetCredentials(ch.Id)
				if e != nil || validateBeeNexAssetContext(a, credentials) != nil {
					return false
				}
			}
			if _, _, e := SeedanceResourceKey(ch, a.KeyFingerprint); e != nil {
				return false
			}
		}
		return true
	}
	var ch *model.Channel
	if fixed, ok := common.GetContextKey(c, constant.ContextKeyTokenSpecificChannelId); ok && !c.GetBool("seedance_unified_routing") {
		id, _ := strconv.Atoi(fmt.Sprint(fixed))
		ch, err = model.GetChannelById(id, true)
		if err == nil && !eligible(ch) {
			ch = nil
		}
	} else {
		ch, err = selectUnifiedPricedChannel(name, banned, eligible, ProviderPolicyForModel(c, name))
	}
	if err != nil || ch == nil {
		return nil, seedanceError(409, "No ready material replicas support this model and request; query material readiness", "asset_not_ready")
	}
	first, err := unifiedSeedanceCopy(c, ids[0], name, ch.Id)
	if err != nil {
		return nil, err
	}
	if !c.GetBool("seedance_unified_routing") {
		_, explicit := common.GetContextKey(c, constant.ContextKeyTokenSpecificChannelId)
		c.Set("seedance_unified_explicit_channel", explicit)
		base, _ := ratio_setting.GetVideoModelBasePrice(name)
		price, e := ChannelBaseUserPriceResolved(ch.Id, name, base)
		if e != nil {
			return nil, e
		}
		c.Set("seedance_unified_price_cap", price)
	}
	c.Set("seedance_unified_routing", true)
	c.Set("seedance_unified_fields", fields)
	c.Set("seedance_unified_copy_channel", ch.Id)
	return first, nil
}

// Material availability is account/request-specific: never reuse the general
// model-only route cache across different sets of referenced assets.
func selectUnifiedPricedChannel(name string, banned []int, filter model.ChannelPickFilter, policy *dto.ProviderSetting) (*model.Channel, error) {
	excluded := append([]int{}, banned...)
	for attempt := 0; attempt < 128; attempt++ {
		id := selectPricedChannelIDFromDBScoped(name, excluded, true, nil, policy)
		if id == 0 {
			return nil, ErrNoCheapestChannel
		}
		ch, err := model.GetChannelById(id, true)
		if err != nil {
			return nil, err
		}
		if filter == nil || filter(ch) {
			return ch, nil
		}
		excluded = append(excluded, id)
	}
	return nil, ErrNoCheapestChannel
}
