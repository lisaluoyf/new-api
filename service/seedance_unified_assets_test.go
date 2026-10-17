package service

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func unifiedRoutingFixture(t *testing.T) (*gorm.DB, *gin.Context) {
	db := seedanceTestDB(t)
	// SQLite :memory: databases belong to one connection; keep migrations and
	// callback/poller assertions on that same database throughout the fixture.
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.ChannelModelPricing{}, &model.SeedanceChannelResolution{}))
	base := "https://apimart.ai"
	a, b := `{"model_group_ratios":{"seedance-2.0-fast":0.5}}`, `{"model_group_ratios":{"seedance-2.0-fast":0.6}}`
	ra, rb := `{"seedance-2.0-fast":1.2}`, `{"seedance-2.0-fast":1}`
	priority := int64(100)
	for _, ch := range []model.Channel{{Id: 7, BaseURL: &base, Setting: &a, ModelPriceRatios: &ra, Key: "account-a", Status: 1, Priority: &priority, Models: "seedance-2.0-fast", Group: "default"}, {Id: 8, BaseURL: &base, Setting: &b, ModelPriceRatios: &rb, Key: "account-b", Status: 1, Priority: &priority, Models: "seedance-2.0-fast", Group: "default"}} {
		require.NoError(t, db.Create(&ch).Error)
		require.NoError(t, db.Create(&model.Ability{ChannelId: ch.Id, Model: "seedance-2.0-fast", Group: "default", Enabled: true, Priority: &priority}).Error)
	}
	for _, id := range []string{"logical-a", "logical-b"} {
		require.NoError(t, db.Create(&model.SeedanceResource{ID: id, Kind: "asset", UserID: 1, GroupType: unifiedSeedanceKind, Status: "Active", RequestData: `{"models":{"seedance-2.0-fast":[7,8]}}`}).Error)
		for _, ch := range []int{7, 8} {
			key := "account-a"
			if ch == 8 {
				key = "account-b"
			}
			require.NoError(t, db.Create(&model.SeedanceResource{ID: id + "-" + key, Kind: "asset", ParentID: id, UserID: 1, GroupType: "virtual", ChannelID: ch, KeyFingerprint: SeedanceKeyFingerprint(key), Model: "seedance-2.0-fast", Status: "Active", UpstreamID: id + "-upstream-" + key}).Error)
		}
	}
	model.InitChannelCache()
	c := seedanceContext(1)
	c.Request = httptest.NewRequest("POST", "/v1/videos/quote", strings.NewReader(`{"model":"seedance-2.0-fast","resolution":"480p"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "seedance-2.0-fast")
	return db, c
}
func TestUnifiedMaterialsSameIDsSelectAndResolveDifferentAccounts(t *testing.T) {
	_, c := unifiedRoutingFixture(t)
	fields := map[string]any{"model": "seedance-2.0-fast", "resolution": "480p", "image_urls": []any{"asset://logical-a", "asset://logical-b"}}
	a, err := chooseUnifiedSeedanceRoute(c, fields, nil)
	require.NoError(t, err)
	require.Equal(t, 7, a.ChannelID)
	resolved, err := ResolveSeedanceAssetReferences(c, fields, 7, "account-a")
	require.NoError(t, err)
	require.Equal(t, []any{"asset://logical-a-upstream-account-a", "asset://logical-b-upstream-account-a"}, resolved["image_urls"])
	a, err = chooseUnifiedSeedanceRoute(c, fields, []int{7})
	require.NoError(t, err)
	require.Equal(t, 8, a.ChannelID)
	resolved, err = ResolveSeedanceAssetReferences(c, fields, 8, "account-b")
	require.NoError(t, err)
	require.Equal(t, []any{"asset://logical-a-upstream-account-b", "asset://logical-b-upstream-account-b"}, resolved["image_urls"])
	_, err = ResolveSeedanceAssetReferences(c, fields, 8, "account-a")
	require.Error(t, err)
	require.Equal(t, []any{"asset://logical-a", "asset://logical-b"}, fields["image_urls"])
}
func TestUnifiedMaterialIntersectionReadinessAndOwnership(t *testing.T) {
	db, c := unifiedRoutingFixture(t)
	require.NoError(t, db.Model(&model.SeedanceResource{}).Where("id = ?", "logical-b-account-b").Update("status", "Pending").Error)
	fields := map[string]any{"model": "seedance-2.0-fast", "image_urls": []any{"asset://logical-a", "asset://logical-b"}}
	_, err := chooseUnifiedSeedanceRoute(c, fields, []int{7})
	require.Error(t, err)
	c.Set("id", 2)
	_, err = chooseUnifiedSeedanceRoute(c, fields, nil)
	require.Error(t, err)
	c.Set("id", 1)
	_, err = chooseUnifiedSeedanceRoute(c, map[string]any{"model": "seedance-2.0-fast", "image_urls": []any{"asset://logical-a-account-a"}}, nil)
	require.Error(t, err)
}
func TestUnifiedMaterialsHigherPriceFallbackIsExcluded(t *testing.T) {
	db, c := unifiedRoutingFixture(t)
	fields := map[string]any{"model": "seedance-2.0-fast", "image_urls": []any{"asset://logical-a"}}
	a, err := chooseUnifiedSeedanceRoute(c, fields, nil)
	require.NoError(t, err)
	require.Equal(t, 7, a.ChannelID)
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 8).Update("model_price_ratios", `{"seedance-2.0-fast":2}`).Error)
	_, err = chooseUnifiedSeedanceRoute(c, fields, []int{7})
	require.Error(t, err)
}
func TestStablePriceTieUsesCostBeforePriorityAndID(t *testing.T) {
	require.True(t, pricedRouteBeats(.072576, .06048, 100, 272, .072576, .072576, 100, 273, true))
	require.False(t, pricedRouteBeats(.072576, .072576, 100, 273, .072576, .06048, 100, 272, true))
	require.False(t, pricedRouteBeats(.08, .01, 100, 272, .072576, .072576, 100, 273, true))
	require.True(t, pricedRouteBeats(1, 1, 100, 7, 1, 1, 100, 8, true))
}
func TestExplicitSeedanceRejectionNeverTreatsUnknownAsRetryable(t *testing.T) {
	require.True(t, SeedanceExplicitRateRejection(429, []byte(`{"error":{"code":"rate_limit","message":"Too many requests"}}`)))
	require.False(t, SeedanceExplicitRateRejection(504, []byte(`{"error":"timeout"}`)))
	require.False(t, SeedanceExplicitRateRejection(429, []byte(`{"data":{"task_id":"accepted"},"error":"rate_limit"}`)))
	require.False(t, SeedanceExplicitRateRejection(200, []byte(`{"error":"rate_limit"}`)))
	require.False(t, SeedanceExplicitRateRejection(429, []byte(`not json`)))
}

func TestUnifiedUploadOnceCreatesPerModelBindingsAndReplaysWithoutUpload(t *testing.T) {
	created := 0
	db := bytePlusTestLibrary(t, func(w http.ResponseWriter, r *http.Request) {
		action, fields := bytePlusTestRequest(t, r)
		switch action {
		case "CreateAssetGroup":
			fmt.Fprint(w, `{"Result":{"Id":"private-group"}}`)
		case "CreateAsset":
			created++
			fmt.Fprintf(w, `{"Result":{"Id":"private-copy-%d"}}`, created)
		case "GetAsset":
			require.NotEmpty(t, fields["Id"])
			fmt.Fprint(w, `{"Result":{"Status":"Active"}}`)
		default:
			t.Fatalf("unexpected action %s", action)
		}
	})
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 283).Update("models", strings.Join(unifiedSeedanceModels, ",")).Error)
	for _, name := range unifiedSeedanceModels {
		if name != "seedance-2.5" {
			require.NoError(t, db.Create(&model.Ability{ChannelId: 283, Group: "default", Model: name, Enabled: true}).Error)
		}
	}
	model.InitChannelCache()
	c := seedanceContext(1)
	c.Request.Header.Set("Idempotency-Key", "one-unified-material")
	plan, err := unifiedSeedanceCurrentPlan(c)
	require.NoError(t, err)
	require.Len(t, plan.Models, 4)
	group, err := createUnifiedSeedanceGroup(c, "seedance-2.5", SeedanceGroupInput{Name: "Virtual"})
	require.NoError(t, err)
	input := SeedanceAssetSubmission{Model: "seedance-2.5", GroupID: group.ID, Assets: []SeedanceAssetInput{{URL: "https://example.com/cartoon.png"}}}
	task, err := SubmitSeedanceAssets(c, input)
	require.NoError(t, err)
	require.Equal(t, "completed", task.Status)
	require.Equal(t, 4, created)
	again, err := SubmitSeedanceAssets(c, input)
	require.NoError(t, err)
	require.Equal(t, task.ID, again.ID)
	require.Equal(t, 4, created)
	var ids []string
	require.NoError(t, common.UnmarshalJsonStr(task.RequestData, &ids))
	require.Len(t, ids, 1)
	asset, err := model.GetSeedanceResource(1, "asset", ids[0])
	require.NoError(t, err)
	dto := SeedanceAssetDTO(asset)
	require.Len(t, dto["available_models"], 4)
	require.Equal(t, true, dto["all_models_ready"])
	data, err := common.Marshal(dto)
	require.NoError(t, err)
	require.NotContains(t, string(data), "private-copy")
	require.NotContains(t, string(data), "inference-key")
	input.Assets[0].URL = "https://example.com/different.png"
	_, err = SubmitSeedanceAssets(c, input)
	require.Error(t, err)
	require.Equal(t, 4, created)
}

func TestUnifiedLogicalAssetPassesNormalizationWithoutProviderID(t *testing.T) {
	_, c := unifiedRoutingFixture(t)
	a, err := seedanceOwnedAsset(c, "logical-a")
	require.NoError(t, err)
	require.Empty(t, a.UpstreamID)
	_, err = seedanceOwnedAsset(c, "logical-a-account-a")
	require.Error(t, err)
}

func TestRejectedUnifiedRequestWithoutReadyFallbackRemainsNotAccepted(t *testing.T) {
	db, c := unifiedRoutingFixture(t)
	require.NoError(t, db.AutoMigrate(&model.VideoSubmission{}))
	fields := map[string]any{"model": "seedance-2.0-fast", "image_urls": []any{"asset://logical-a"}}
	_, err := chooseUnifiedSeedanceRoute(c, fields, nil)
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 8).Update("model_price_ratios", `{"seedance-2.0-fast":2}`).Error)
	record, fresh, err := model.ClaimVideoSubmission(&model.VideoSubmission{ID: "rejected-no-backup", UserID: 1, Fingerprint: "same", TaskID: "one-task"})
	require.NoError(t, err)
	require.True(t, fresh)
	require.NoError(t, model.MarkVideoSubmissionSending(record.ID, 7))
	c.Set(VideoSubmissionContextKey, record)
	c.Set(VideoSubmissionSentContextKey, true)
	c.Set("seedance_confirmed_not_accepted", true)
	c.Set("use_channel", []string{"7"})
	require.Error(t, ResetRejectedUnifiedSubmission(c, 7))
	saved, err := model.GetVideoSubmission(1, record.ID)
	require.NoError(t, err)
	require.Equal(t, model.VideoSubmissionNotAccepted, saved.Status)
	require.False(t, c.GetBool(VideoSubmissionSentContextKey))
}
