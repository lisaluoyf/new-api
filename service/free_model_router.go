package service

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

const freeModelPlanContextKey = "free_model_candidate_plan"

const (
	freeModelAttemptNumberKey = "free_model_attempt_number"
	freeModelAttemptStartKey  = "free_model_attempt_start"
	freeModelAttemptLoggedKey = "free_model_attempt_logged"
)

var ErrFreeModelCapabilityUnavailable = errors.New("No free model currently supports the requested capabilities")

type FreeModelCandidate struct {
	ChannelID        int                   `json:"channel_id"`
	ChannelName      string                `json:"channel_name,omitempty"`
	UpstreamModel    string                `json:"upstream_model"`
	Priority         int64                 `json:"priority"`
	Weight           uint                  `json:"weight"`
	TimeoutMS        int                   `json:"timeout_ms"`
	Health           FreeModelHealth       `json:"health"`
	RecoveryAt       int64                 `json:"recovery_at,omitempty"`
	RecoveryFallback bool                  `json:"recovery_fallback,omitempty"`
	Channel          *model.Channel        `json:"-"`
	Config           model.FreeModelMember `json:"-"`
}

type FreeModelFilteredCandidate struct {
	ChannelID     int      `json:"channel_id"`
	UpstreamModel string   `json:"upstream_model,omitempty"`
	Reasons       []string `json:"reasons"`
}

type FreeModelAttempt struct {
	Attempt       int    `json:"attempt"`
	ChannelID     int    `json:"channel_id"`
	UpstreamModel string `json:"upstream_model"`
	StatusCode    int    `json:"status_code,omitempty"`
	ErrorCode     string `json:"error_code,omitempty"`
	DurationMS    int64  `json:"duration_ms"`
	Result        string `json:"result"`
}

type FreeModelRouteTrace struct {
	RequestedModel        string                       `json:"requested_model"`
	RequiredCapabilities  []string                     `json:"required_capabilities"`
	CandidateModels       []FreeModelCandidate         `json:"candidate_models"`
	FilteredCandidates    []FreeModelFilteredCandidate `json:"filtered_candidates"`
	SelectedChannelID     int                          `json:"selected_channel_id,omitempty"`
	ResolvedUpstreamModel string                       `json:"resolved_upstream_model,omitempty"`
	Attempts              []FreeModelAttempt           `json:"attempts"`
	FinalResult           string                       `json:"final_result,omitempty"`
}

type FreeModelCandidatePlan struct {
	mu           sync.Mutex
	Requirements FreeModelRequirements
	Candidates   []FreeModelCandidate
	Filtered     []FreeModelFilteredCandidate
	MaxAttempts  int
	next         int
	Trace        FreeModelRouteTrace
}

type FreeModelRandom interface{ Int63n(n int64) int64 }

func BuildFreeModelCandidatePlan(requirements FreeModelRequirements, rng FreeModelRandom) (*FreeModelCandidatePlan, error) {
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	var channels []model.Channel
	if err := model.DB.Where("status = ?", common.ChannelStatusEnabled).Find(&channels).Error; err != nil {
		return nil, err
	}
	plan := &FreeModelCandidatePlan{Requirements: requirements, MaxAttempts: GetFreeModelSettings().MaxAttempts}
	plan.Trace.RequestedModel = FreeModelID
	plan.Trace.RequiredCapabilities = requirements.Names()

	memberChannels := make([]model.Channel, 0)
	channelIDs := make([]int, 0)
	for i := range channels {
		channel := &channels[i]
		if !common.StringsContains(channel.GetModels(), FreeModelID) {
			continue
		}
		upstream := ModelMappingTarget(channel.ModelMapping, FreeModelID)
		if upstream == "" {
			plan.Filtered = append(plan.Filtered, FreeModelFilteredCandidate{ChannelID: channel.Id, Reasons: []string{"missing_model_mapping"}})
			continue
		}
		var count int64
		if err := model.DB.Model(&model.Ability{}).Where("channel_id = ? AND model = ? AND enabled = ?", channel.Id, FreeModelID, true).Count(&count).Error; err != nil {
			return nil, err
		}
		if count == 0 {
			plan.Filtered = append(plan.Filtered, FreeModelFilteredCandidate{ChannelID: channel.Id, UpstreamModel: upstream, Reasons: []string{"ability_disabled"}})
			continue
		}
		memberChannels = append(memberChannels, *channel)
		channelIDs = append(channelIDs, channel.Id)
	}
	configs, err := model.GetFreeModelMembers(channelIDs)
	if err != nil {
		return nil, err
	}
	now := freeModelHealthNow()
	capable := make([]FreeModelCandidate, 0, len(memberChannels))
	avoided := make([]FreeModelCandidate, 0)
	for i := range memberChannels {
		channel := &memberChannels[i]
		cfg := configs[channel.Id]
		upstream := ModelMappingTarget(channel.ModelMapping, FreeModelID)
		reasons := freeModelConfigMismatch(cfg, requirements)
		if len(reasons) > 0 {
			plan.Filtered = append(plan.Filtered, FreeModelFilteredCandidate{ChannelID: channel.Id, UpstreamModel: upstream, Reasons: reasons})
			continue
		}
		health := GetFreeModelHealth(channel.Id)
		candidate := FreeModelCandidate{ChannelID: channel.Id, ChannelName: channel.Name, UpstreamModel: upstream, Priority: cfg.Priority, Weight: cfg.Weight, TimeoutMS: cfg.TimeoutMS, Health: health, RecoveryAt: health.AvoidUntil(), Channel: channel, Config: cfg}
		if health.IsAvoided(now) {
			avoided = append(avoided, candidate)
		} else {
			capable = append(capable, candidate)
		}
	}
	if len(capable) == 0 && len(avoided) > 0 {
		sort.Slice(avoided, func(i, j int) bool { return avoided[i].RecoveryAt < avoided[j].RecoveryAt })
		candidate := avoided[0]
		candidate.RecoveryFallback = true
		capable = append(capable, candidate)
		for _, filtered := range avoided[1:] {
			plan.Filtered = append(plan.Filtered, FreeModelFilteredCandidate{ChannelID: filtered.ChannelID, UpstreamModel: filtered.UpstreamModel, Reasons: []string{"cooldown_or_open_circuit"}})
		}
	} else {
		for _, filtered := range avoided {
			plan.Filtered = append(plan.Filtered, FreeModelFilteredCandidate{ChannelID: filtered.ChannelID, UpstreamModel: filtered.UpstreamModel, Reasons: []string{"cooldown_or_open_circuit"}})
		}
	}
	if len(capable) == 0 {
		plan.Trace.FilteredCandidates = plan.Filtered
		return plan, ErrFreeModelCapabilityUnavailable
	}
	plan.Candidates = orderFreeModelCandidates(capable, rng)
	plan.Trace.CandidateModels = append([]FreeModelCandidate(nil), plan.Candidates...)
	plan.Trace.FilteredCandidates = append([]FreeModelFilteredCandidate(nil), plan.Filtered...)
	return plan, nil
}

func freeModelConfigMismatch(cfg model.FreeModelMember, req FreeModelRequirements) []string {
	reasons := make([]string, 0)
	if !cfg.Enabled {
		reasons = append(reasons, "member_disabled")
	}
	if req.Text && !cfg.Text {
		reasons = append(reasons, "text_unsupported")
	}
	if req.Vision && !cfg.Vision {
		reasons = append(reasons, "vision_unsupported")
	}
	if req.Tools && !cfg.Tools {
		reasons = append(reasons, "tools_unsupported")
	}
	if req.JSONObject && !cfg.JSONObject {
		reasons = append(reasons, "json_object_unsupported")
	}
	if req.JSONSchema && !cfg.JSONSchema {
		reasons = append(reasons, "json_schema_unsupported")
	}
	if cfg.MaxContextTokens > 0 && req.TotalContextTokens() > cfg.MaxContextTokens {
		reasons = append(reasons, "context_length_exceeded")
	}
	return reasons
}

func orderFreeModelCandidates(candidates []FreeModelCandidate, rng FreeModelRandom) []FreeModelCandidate {
	byPriority := make(map[int64][]FreeModelCandidate)
	priorities := make([]int64, 0)
	for _, candidate := range candidates {
		if _, exists := byPriority[candidate.Priority]; !exists {
			priorities = append(priorities, candidate.Priority)
		}
		byPriority[candidate.Priority] = append(byPriority[candidate.Priority], candidate)
	}
	sort.Slice(priorities, func(i, j int) bool { return priorities[i] > priorities[j] })
	ordered := make([]FreeModelCandidate, 0, len(candidates))
	for _, priority := range priorities {
		remaining := append([]FreeModelCandidate(nil), byPriority[priority]...)
		for len(remaining) > 0 {
			var total int64
			for _, candidate := range remaining {
				total += int64(candidate.Weight)
			}
			index := 0
			if total == 0 {
				index = int(rng.Int63n(int64(len(remaining))))
			} else {
				draw := rng.Int63n(total)
				for i, candidate := range remaining {
					draw -= int64(candidate.Weight)
					if draw < 0 {
						index = i
						break
					}
				}
			}
			ordered = append(ordered, remaining[index])
			remaining = append(remaining[:index], remaining[index+1:]...)
		}
	}
	return ordered
}

func (p *FreeModelCandidatePlan) Next() (*FreeModelCandidate, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	limit := p.MaxAttempts
	if limit <= 0 {
		limit = GetFreeModelSettings().MaxAttempts
	}
	if p.next >= len(p.Candidates) || p.next >= limit {
		return nil, fmt.Errorf("free model candidate plan exhausted")
	}
	candidate := p.Candidates[p.next]
	p.next++
	p.Trace.SelectedChannelID = candidate.ChannelID
	p.Trace.ResolvedUpstreamModel = candidate.UpstreamModel
	return &candidate, nil
}

func (p *FreeModelCandidatePlan) HasNext() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	limit := p.MaxAttempts
	if limit <= 0 {
		limit = GetFreeModelSettings().MaxAttempts
	}
	return p.next < len(p.Candidates) && p.next < limit
}

func FreeModelPlanHasNext(c *gin.Context) bool {
	plan, ok := GetFreeModelCandidatePlan(c)
	return ok && plan.HasNext()
}

func SetFreeModelCandidatePlan(c *gin.Context, plan *FreeModelCandidatePlan) {
	c.Set(freeModelPlanContextKey, plan)
}

func GetFreeModelCandidatePlan(c *gin.Context) (*FreeModelCandidatePlan, bool) {
	value, ok := c.Get(freeModelPlanContextKey)
	if !ok {
		return nil, false
	}
	plan, ok := value.(*FreeModelCandidatePlan)
	return plan, ok
}

func SelectFreeModelChannel(c *gin.Context) (*model.Channel, error) {
	plan, ok := GetFreeModelCandidatePlan(c)
	if !ok {
		return nil, errors.New("free model candidate plan is missing")
	}
	candidate, err := plan.Next()
	if err != nil {
		return nil, err
	}
	return candidate.Channel, nil
}

func FreeModelCandidateForChannel(c *gin.Context, channelID int) (FreeModelCandidate, bool) {
	plan, ok := GetFreeModelCandidatePlan(c)
	if !ok {
		return FreeModelCandidate{}, false
	}
	for _, candidate := range plan.Candidates {
		if candidate.ChannelID == channelID {
			return candidate, true
		}
	}
	return FreeModelCandidate{}, false
}

func FreeModelRequirementsFromContext(c *gin.Context) (FreeModelRequirements, bool) {
	plan, ok := GetFreeModelCandidatePlan(c)
	if !ok {
		return FreeModelRequirements{}, false
	}
	return plan.Requirements, true
}

func AppendFreeModelAttempt(c *gin.Context, attempt FreeModelAttempt) {
	plan, ok := GetFreeModelCandidatePlan(c)
	if !ok {
		return
	}
	plan.mu.Lock()
	defer plan.mu.Unlock()
	plan.Trace.Attempts = append(plan.Trace.Attempts, attempt)
}

func BeginFreeModelAttempt(c *gin.Context, attempt int, startedAt time.Time) {
	if c == nil {
		return
	}
	c.Set(freeModelAttemptNumberKey, attempt)
	c.Set(freeModelAttemptStartKey, startedAt)
	c.Set(freeModelAttemptLoggedKey, false)
}

// MarkFreeModelAttemptSuccessForLog runs before the consume log is assembled,
// because quota settlement happens inside the relay adaptor before control
// returns to the outer retry loop.
func MarkFreeModelAttemptSuccessForLog(c *gin.Context) {
	if c == nil || c.GetBool(freeModelAttemptLoggedKey) {
		return
	}
	plan, ok := GetFreeModelCandidatePlan(c)
	if !ok {
		return
	}
	channelID := c.GetInt("channel_id")
	candidate, _ := FreeModelCandidateForChannel(c, channelID)
	startedAt := time.Now()
	if value, exists := c.Get(freeModelAttemptStartKey); exists {
		if parsed, valid := value.(time.Time); valid {
			startedAt = parsed
		}
	}
	plan.mu.Lock()
	plan.Trace.Attempts = append(plan.Trace.Attempts, FreeModelAttempt{Attempt: c.GetInt(freeModelAttemptNumberKey), ChannelID: channelID, UpstreamModel: candidate.UpstreamModel, StatusCode: 200, DurationMS: time.Since(startedAt).Milliseconds(), Result: "success"})
	plan.Trace.FinalResult = "success"
	plan.mu.Unlock()
	c.Set(freeModelAttemptLoggedKey, true)
}

func SetFreeModelFinalResult(c *gin.Context, result string) {
	plan, ok := GetFreeModelCandidatePlan(c)
	if !ok {
		return
	}
	plan.mu.Lock()
	plan.Trace.FinalResult = result
	plan.mu.Unlock()
}

func FreeModelTrace(c *gin.Context) (FreeModelRouteTrace, bool) {
	plan, ok := GetFreeModelCandidatePlan(c)
	if !ok {
		return FreeModelRouteTrace{}, false
	}
	plan.mu.Lock()
	defer plan.mu.Unlock()
	return plan.Trace, true
}

func AppendFreeModelRouteAdminInfo(c *gin.Context, adminInfo map[string]interface{}) {
	if adminInfo == nil {
		return
	}
	if trace, ok := FreeModelTrace(c); ok {
		adminInfo["free_model_route"] = trace
	}
}
