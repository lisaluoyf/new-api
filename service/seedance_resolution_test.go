package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"strings"
	"testing"
)

func resolutionContext(body string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/videos", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c
}

func TestSeedanceResolutionSelectionsAndVariants(t *testing.T) {
	db := videoVerificationTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.SeedanceChannelResolution{}))
	require.Len(t, model.SeedanceResolutionOptions("seedance-2.5"), 6)
	require.Len(t, model.SeedanceResolutionOptions("seedance-2.0"), 8)
	require.Len(t, model.SeedanceResolutionOptions("seedance-2.0-fast"), 4)
	require.Error(t, model.SaveSeedanceResolutionSelection(1, "seedance-2.5", []string{"4K"}))
	require.Error(t, model.SaveSeedanceResolutionSelection(1, "seedance-2.5", nil))
	require.NoError(t, model.SaveSeedanceResolutionSelection(1, "seedance-2.5", []string{"480p", "480p"}))
	require.NoError(t, model.SaveSeedanceResolutionSelection(2, "seedance-2.5", []string{}))
	selections, err := model.SeedanceResolutionSelections("seedance-2.5")
	require.NoError(t, err)
	require.Equal(t, []string{"480P"}, selections[1])
	require.Empty(t, selections[2])
	for _, body := range []string{`{"resolution":"480p"}`, `{"size":"480p","image_urls":["https://example.com/image.png"]}`, `{"metadata":{"resolution":"480P"}}`} {
		filter := SeedanceResolutionPickFilter(resolutionContext(body), "seedance-2.5")
		require.True(t, filter(&model.Channel{Id: 1}))
		require.False(t, filter(&model.Channel{Id: 2}))
		require.True(t, filter(&model.Channel{Id: 3}))
	}
	for _, body := range []string{`{"resolution":"480p","video_urls":["https://example.com/video.mp4"]}`, `{"resolution":"480p","content":[{"type":"video_url","video_url":{"url":"asset://reference"}}]}`, `{"metadata":{"resolution":"480p","has_video":true}}`} {
		filter := SeedanceResolutionPickFilter(resolutionContext(body), "seedance-2.5")
		require.False(t, filter(&model.Channel{Id: 1}))
		require.True(t, filter(&model.Channel{Id: 3}))
	}
	c := resolutionContext(`{"resolution":"480p"}`)
	filter := SeedanceResolutionPickFilter(c, "seedance-2.5")
	require.NoError(t, model.SaveSeedanceResolutionSelection(1, "seedance-2.5", []string{}))
	require.True(t, filter(&model.Channel{Id: 1}), "request snapshot remains consistent across retries")
	require.False(t, SeedanceResolutionPickFilter(resolutionContext(`{"resolution":"480p"}`), "seedance-2.5")(&model.Channel{Id: 1}), "next request sees changes immediately")
	c = resolutionContext(`{"draft":true}`)
	c.Set("seedance25_normalized_request", map[string]any{"resolution": "480p"})
	variantDraft, errDraft := seedanceRoutingVariant(c)
	require.NoError(t, errDraft)
	require.Equal(t, "480P", variantDraft)
	c.Request = httptest.NewRequest("GET", "/v1/videos/task", nil)
	require.Nil(t, SeedanceResolutionPickFilter(c, "seedance-2.5"))
	c = resolutionContext(`{"resolution":"1080p"}`)
	c.Set("seedance_draft_task", &model.Task{PrivateData: model.TaskPrivateData{SeedanceRequest: map[string]any{"video_input_seconds": 5}}})
	variant, err := seedanceRoutingVariant(c)
	require.NoError(t, err)
	require.Equal(t, "1080P-input", variant)
}

func TestSeedanceResolutionFallbackBeforePriority(t *testing.T) {
	db := videoVerificationTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.SeedanceChannelResolution{}))
	oldMemory := common.MemoryCacheEnabled
	t.Cleanup(func() { common.MemoryCacheEnabled = oldMemory; model.InitChannelCache() })
	for id := 1; id <= 3; id++ {
		priority := int64(100 - id)
		channel := model.Channel{Id: id, Name: "resolution-test", Key: "private-test", Models: "seedance-2.5", Group: "sd-test", Status: 1, Priority: &priority}
		require.NoError(t, db.Create(&channel).Error)
		require.NoError(t, channel.AddAbilities(nil))
	}
	require.NoError(t, model.SaveSeedanceResolutionSelection(1, "seedance-2.5", []string{"720P"}))
	require.NoError(t, model.SaveSeedanceResolutionSelection(2, "seedance-2.5", []string{"480P"}))
	for _, memory := range []bool{true} {
		common.MemoryCacheEnabled = memory
		if memory {
			model.InitChannelCache()
		}
		c := resolutionContext(`{"resolution":"480p"}`)
		channel, group, err := CacheGetRandomSatisfiedChannel(&RetryParam{Ctx: c, TokenGroup: "sd-test", ModelName: "seedance-2.5"})
		require.NoError(t, err)
		require.Equal(t, "sd-test", group)
		require.NotNil(t, channel)
		require.Equal(t, 2, channel.Id)
		retry := 1
		channel, _, err = CacheGetRandomSatisfiedChannel(&RetryParam{Ctx: c, TokenGroup: "sd-test", ModelName: "seedance-2.5", Retry: &retry})
		require.NoError(t, err)
		require.NotNil(t, channel)
		require.Equal(t, 3, channel.Id)
		c = resolutionContext(`{"resolution":"480p","video_urls":["https://example.com/input.mp4"]}`)
		channel, _, err = CacheGetRandomSatisfiedChannel(&RetryParam{Ctx: c, TokenGroup: "sd-test", ModelName: "seedance-2.5"})
		require.NoError(t, err)
		require.NotNil(t, channel)
		require.Equal(t, 3, channel.Id)
	}
}

func TestVideoVerificationTechnicalFailureAlsoAlerts(t *testing.T) {
	db := videoVerificationTestDB(t)
	task := &model.Task{ID: 81, ChannelId: 7}
	state, claimed, err := claimVideoVerification(t.Context(), task, "seedance-2.0", 1000, 1000)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, finishVideoVerification(t.Context(), state, task, "notcomplete", "video_download_failed", nil, 1001))
	var alert model.VideoVerificationAlert
	require.NoError(t, db.First(&alert).Error)
	require.Equal(t, "video_download_failed", alert.Reason)
	require.Equal(t, "seedance-2.0", alert.Model)
	require.EqualValues(t, 1001, alert.DetectedAt)
}
