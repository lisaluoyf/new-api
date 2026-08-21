package service

import (
	"fmt"
	"math/rand"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupFreeModelRouterDB(t *testing.T) *gorm.DB {
	t.Helper()
	oldDB, oldRedis := model.DB, common.RedisEnabled
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	common.RedisEnabled = false
	resetFreeModelHealthForTest()
	resetFreeModelDailyLimitForTest()
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.FreeModelMember{}))
	t.Cleanup(func() {
		model.DB = oldDB
		common.RedisEnabled = oldRedis
		resetFreeModelHealthForTest()
		resetFreeModelDailyLimitForTest()
	})
	return db
}

func addFreeMember(t *testing.T, db *gorm.DB, id int, cfg model.FreeModelMember) {
	t.Helper()
	mapping := fmt.Sprintf(`{"%s":"provider/model-%d"}`, FreeModelID, id)
	channel := model.Channel{Id: id, Name: fmt.Sprintf("free-%d", id), Key: "secret", Models: FreeModelID, ModelMapping: &mapping, Status: common.ChannelStatusEnabled, Group: "default"}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: FreeModelID, ChannelId: id, Enabled: true}).Error)
	cfg.ChannelID = id
	require.NoError(t, db.Create(&cfg).Error)
}

func fullMember() model.FreeModelMember {
	cfg := model.DefaultFreeModelMember(0)
	cfg.Vision, cfg.Tools, cfg.JSONObject, cfg.JSONSchema = true, true, true, true
	return cfg
}

func TestBuildFreeModelCandidatePlanCapabilityAndContextFiltering(t *testing.T) {
	db := setupFreeModelRouterDB(t)
	text := model.DefaultFreeModelMember(1)
	addFreeMember(t, db, 1, text)
	all := fullMember()
	all.MaxContextTokens = 100
	addFreeMember(t, db, 2, all)

	plan, err := BuildFreeModelCandidatePlan(FreeModelRequirements{Text: true, Vision: true, Tools: true, JSONObject: true, JSONSchema: true, EstimatedInput: 60, RequestedOutput: 20}, rand.New(rand.NewSource(7)))
	require.NoError(t, err)
	require.Len(t, plan.Candidates, 1)
	require.Equal(t, 2, plan.Candidates[0].ChannelID)
	require.Contains(t, plan.Filtered[0].Reasons, "vision_unsupported")

	_, err = BuildFreeModelCandidatePlan(FreeModelRequirements{Text: true, Vision: true, EstimatedInput: 90, RequestedOutput: 20}, rand.New(rand.NewSource(7)))
	require.ErrorIs(t, err, ErrFreeModelCapabilityUnavailable)
}

func TestFreeModelVisionOnlyMemberNeverReceivesPlainText(t *testing.T) {
	db := setupFreeModelRouterDB(t)
	visionOnly := model.DefaultFreeModelMember(1)
	visionOnly.Text = false
	visionOnly.Vision = true
	addFreeMember(t, db, 1, visionOnly)

	_, err := BuildFreeModelCandidatePlan(FreeModelRequirements{Text: true}, rand.New(rand.NewSource(7)))
	require.ErrorIs(t, err, ErrFreeModelCapabilityUnavailable)

	plan, err := BuildFreeModelCandidatePlan(FreeModelRequirements{Text: true, Vision: true}, rand.New(rand.NewSource(7)))
	require.NoError(t, err)
	require.Len(t, plan.Candidates, 1)
	require.Equal(t, 1, plan.Candidates[0].ChannelID)
}

func TestFreeModelDailyLimitFiltersExhaustedMember(t *testing.T) {
	db := setupFreeModelRouterDB(t)
	limited := fullMember()
	limited.DailyRequestLimit = 2
	addFreeMember(t, db, 1, limited)
	unlimited := fullMember()
	addFreeMember(t, db, 2, unlimited)

	allowed, used, err := ReserveFreeModelDailyRequest(1, 2)
	require.NoError(t, err)
	require.True(t, allowed)
	require.Equal(t, 1, used)
	allowed, used, err = ReserveFreeModelDailyRequest(1, 2)
	require.NoError(t, err)
	require.True(t, allowed)
	require.Equal(t, 2, used)

	plan, err := BuildFreeModelCandidatePlan(FreeModelRequirements{Text: true}, rand.New(rand.NewSource(7)))
	require.NoError(t, err)
	require.Len(t, plan.Candidates, 1)
	require.Equal(t, 2, plan.Candidates[0].ChannelID)
	require.Contains(t, plan.Filtered[0].Reasons, "daily_request_limit_exhausted")
}

func TestFreeModelDailyLimitRaceSkipsToNextCandidate(t *testing.T) {
	db := setupFreeModelRouterDB(t)
	limited := fullMember()
	limited.Priority = 200
	limited.DailyRequestLimit = 1
	addFreeMember(t, db, 1, limited)
	unlimited := fullMember()
	unlimited.Priority = 100
	addFreeMember(t, db, 2, unlimited)

	plan, err := BuildFreeModelCandidatePlan(FreeModelRequirements{Text: true}, rand.New(rand.NewSource(7)))
	require.NoError(t, err)
	allowed, _, err := ReserveFreeModelDailyRequest(1, 1)
	require.NoError(t, err)
	require.True(t, allowed)

	candidate, err := plan.Next()
	require.NoError(t, err)
	require.Equal(t, 2, candidate.ChannelID)
	require.Contains(t, plan.Trace.FilteredCandidates[len(plan.Trace.FilteredCandidates)-1].Reasons, "daily_request_limit_exhausted")
}

func TestFreeModelPriorityWeightStableWithoutReplacement(t *testing.T) {
	db := setupFreeModelRouterDB(t)
	for _, data := range []struct {
		id       int
		priority int64
		weight   uint
	}{{1, 200, 1}, {2, 200, 100}, {3, 100, 100}} {
		cfg := fullMember()
		cfg.Priority, cfg.Weight = data.priority, data.weight
		addFreeMember(t, db, data.id, cfg)
	}
	planA, err := BuildFreeModelCandidatePlan(FreeModelRequirements{Text: true}, rand.New(rand.NewSource(42)))
	require.NoError(t, err)
	planB, err := BuildFreeModelCandidatePlan(FreeModelRequirements{Text: true}, rand.New(rand.NewSource(42)))
	require.NoError(t, err)
	ids := func(p *FreeModelCandidatePlan) []int {
		out := []int{}
		for _, c := range p.Candidates {
			out = append(out, c.ChannelID)
		}
		return out
	}
	require.Equal(t, ids(planA), ids(planB))
	require.ElementsMatch(t, []int{1, 2}, ids(planA)[:2])
	require.Equal(t, 3, ids(planA)[2])
	seen := map[int]bool{}
	for range 3 {
		candidate, nextErr := planA.Next()
		require.NoError(t, nextErr)
		require.False(t, seen[candidate.ChannelID])
		seen[candidate.ChannelID] = true
	}
}

func TestFreeModelWeightInfluencesFirstChoiceWithDeterministicSeeds(t *testing.T) {
	candidates := []FreeModelCandidate{
		{ChannelID: 1, Priority: 100, Weight: 1},
		{ChannelID: 2, Priority: 100, Weight: 9},
	}
	heavyFirst := 0
	for seed := int64(0); seed < 1000; seed++ {
		ordered := orderFreeModelCandidates(candidates, rand.New(rand.NewSource(seed)))
		if ordered[0].ChannelID == 2 {
			heavyFirst++
		}
	}
	require.Greater(t, heavyFirst, 800)
}

func TestFreeModelNeverIncludesNonMemberPaidChannel(t *testing.T) {
	db := setupFreeModelRouterDB(t)
	cfg := fullMember()
	addFreeMember(t, db, 1, cfg)
	paid := model.Channel{Id: 99, Name: "paid", Key: "paid-key", Models: "gpt-4o", Status: common.ChannelStatusEnabled, Group: "default"}
	require.NoError(t, db.Create(&paid).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "gpt-4o", ChannelId: 99, Enabled: true}).Error)
	plan, err := BuildFreeModelCandidatePlan(FreeModelRequirements{Text: true}, rand.New(rand.NewSource(1)))
	require.NoError(t, err)
	require.Len(t, plan.Candidates, 1)
	require.Equal(t, 1, plan.Candidates[0].ChannelID)
}

func TestFreeModelAllCooldownUsesEarliestRecoveryOnce(t *testing.T) {
	db := setupFreeModelRouterDB(t)
	cfg := fullMember()
	addFreeMember(t, db, 1, cfg)
	addFreeMember(t, db, 2, cfg)
	RecordFreeModelFailure(1, 429, true)
	RecordFreeModelFailure(2, 429, true)
	plan, err := BuildFreeModelCandidatePlan(FreeModelRequirements{Text: true}, rand.New(rand.NewSource(1)))
	require.NoError(t, err)
	require.Len(t, plan.Candidates, 1)
	require.True(t, plan.Candidates[0].RecoveryFallback)
}

func TestFreeModelHealthCooldownCircuitAndRecovery(t *testing.T) {
	_ = setupFreeModelRouterDB(t)
	rateLimited := RecordFreeModelFailure(11, 429, true)
	require.Greater(t, rateLimited.CooldownUntil, time.Now().UnixMilli())
	var failed FreeModelHealth
	for range freeModelCircuitFailures {
		failed = RecordFreeModelFailure(12, 503, true)
	}
	require.Greater(t, failed.CircuitOpenUntil, time.Now().UnixMilli())
	recovered := RecordFreeModelSuccess(12, 25*time.Millisecond)
	require.Equal(t, freeModelCircuitFailures-1, recovered.ConsecutiveFailure)
	require.Positive(t, recovered.EWLatencyMS)
}

func TestFreeModelPlanHonorsIndependentMaxAttempts(t *testing.T) {
	db := setupFreeModelRouterDB(t)
	for id := 1; id <= 4; id++ {
		cfg := fullMember()
		addFreeMember(t, db, id, cfg)
	}
	common.OptionMapRWMutex.Lock()
	mapWasNil := common.OptionMap == nil
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	old, existed := common.OptionMap[FreeModelSettingsOptionKey()]
	common.OptionMap[FreeModelSettingsOptionKey()] = `{"cumulative_paid_enabled":true,"minimum_cumulative_paid_usd":50,"active_subscription_enabled":true,"minimum_subscription_price_usd":20,"account_requests_per_minute":10,"max_attempts":3}`
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if mapWasNil {
			common.OptionMap = nil
		} else if existed {
			common.OptionMap[FreeModelSettingsOptionKey()] = old
		} else {
			delete(common.OptionMap, FreeModelSettingsOptionKey())
		}
		common.OptionMapRWMutex.Unlock()
	})
	plan, err := BuildFreeModelCandidatePlan(FreeModelRequirements{Text: true}, rand.New(rand.NewSource(9)))
	require.NoError(t, err)
	for range 3 {
		_, err = plan.Next()
		require.NoError(t, err)
	}
	_, err = plan.Next()
	require.Error(t, err)
}

func TestFreeModelSuccessTraceIsReadyBeforeConsumeLog(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	candidate := FreeModelCandidate{ChannelID: 2, UpstreamModel: "provider/free"}
	plan := &FreeModelCandidatePlan{Candidates: []FreeModelCandidate{candidate}, Trace: FreeModelRouteTrace{RequestedModel: FreeModelID}}
	SetFreeModelCandidatePlan(c, plan)
	c.Set("channel_id", 2)
	c.Set("use_channel", []string{"1", "2"})
	BeginFreeModelAttempt(c, 2, time.Now().Add(-10*time.Millisecond))
	MarkFreeModelAttemptSuccessForLog(c)
	trace, ok := FreeModelTrace(c)
	require.True(t, ok)
	require.Equal(t, "success", trace.FinalResult)
	require.Len(t, trace.Attempts, 1)
	require.Equal(t, 2, trace.Attempts[0].Attempt)
	other := map[string]interface{}{}
	appendChannelRetryFallbackInfo(c, other, c.GetStringSlice("use_channel"))
	require.Equal(t, true, other["fallback_triggered"])
	admin := map[string]interface{}{}
	AppendFreeModelRouteAdminInfo(c, admin)
	require.Contains(t, admin, "free_model_route")
}
