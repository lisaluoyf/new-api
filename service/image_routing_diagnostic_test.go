package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func imageDiagnosticContext(body string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c
}

func TestImageRoutingDiagnosticPreservesOriginalModelAndRedactsContent(t *testing.T) {
	c := imageDiagnosticContext(`{"model":"gpt-image-2-official","prompt":"private-prompt","user":"private-user","image_urls":["https://private-image"],"response_format":"b64_json","output_format":"private-value"}`)
	c.Request.Header.Set("Authorization", "Bearer private-key")
	c.Request.Header.Set(HeaderApimasterRoutingRetry, "2")
	require.Equal(t, "gpt-image-2", PrepareGptImage2ModelRequest(c, "gpt-image-2-official"))
	trace := imageRoutingTrace(c)
	require.Equal(t, "gpt-image-2-official", trace.OriginalModel)
	require.Equal(t, true, trace.Request["explicit_official"])
	require.Equal(t, "b64_json", trace.Request["response_format"])
	require.Equal(t, true, trace.Request["has_user"])
	require.Equal(t, 1, trace.Request["reference_image_count"])
	require.Equal(t, 2, trace.RoutingRetry)
	require.Empty(t, gptImage2CapabilityRequestFromContext(c, "gpt-image-2").ResponseFormat)

	admin := map[string]interface{}{}
	AppendImageRoutingAdminInfo(c, admin)
	raw, err := common.Marshal(admin)
	require.NoError(t, err)
	for _, secret := range []string{"private-prompt", "private-user", "private-image", "private-key", "private-value"} {
		require.NotContains(t, string(raw), secret)
	}
	RecordImageRoutingSelected(c, 59)
	snapshot := admin["image_routing"].(map[string]interface{})
	require.Empty(t, snapshot["events"])
	for i := 0; i < maxImageRoutingEvents+1; i++ {
		RecordImageRoutingSelected(c, 73)
	}
	require.Len(t, trace.Events, maxImageRoutingEvents)
	require.True(t, trace.Truncated)
}

func TestImageRoutingDiagnosticExplainsCheapestChannelSelection(t *testing.T) {
	InvalidateChannelRoutingCache()
	t.Cleanup(InvalidateChannelRoutingCache)
	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.ChannelModelPricing{}, &model.Ability{}))
	common.OptionMapRWMutex.Lock()
	previous := common.OptionMap
	common.OptionMap = map[string]string{ratio_setting.ImageModelPricingOption: ratio_setting.DefaultImageModelPricingJSON()}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previous
		common.OptionMapRWMutex.Unlock()
	})
	for _, ch := range []struct {
		id            int
		group, markup float64
		official      bool
	}{
		{73, 0.01, 3, false}, {59, 0.8, 1, true},
	} {
		fields := []string{"size", "resolution", "quality"}
		if ch.official {
			fields = append(fields, "output_format")
		}
		settings, err := common.Marshal(map[string]interface{}{
			"gpt_image2_capabilities": &dto.GptImage2Capabilities{
				Enabled: true, Version: 1, OfficialAlias: ch.official,
				Generations: &dto.GptImage2EndpointCapabilities{Enabled: true, MaxN: 4, OptionalFields: fields},
			},
		})
		require.NoError(t, err)
		setting, err := common.Marshal(map[string]float64{"manual_group_ratio": ch.group})
		require.NoError(t, err)
		settingText := string(setting)
		require.NoError(t, db.Create(&model.Channel{
			Id: ch.id, Status: 1, Models: "gpt-image-2", Group: "default",
			Setting: &settingText, OtherSettings: string(settings), ApimasterPriceRatio: &ch.markup,
		}).Error)
		require.NoError(t, db.Create(&model.Ability{ChannelId: ch.id, Model: "gpt-image-2", Group: "default", Enabled: true}).Error)
	}
	for _, tc := range []struct {
		name, body, reason string
		selected           int
	}{
		{"basic", `{"model":"gpt-image-2","n":1,"size":"1024x1024"}`, "", 73},
		{"format", `{"model":"gpt-image-2","output_format":"png"}`, "unsupported_field_or_value:output_format", 59},
		{"official", `{"model":"gpt-image-2-official"}`, "official_alias_not_supported", 59},
		{"2k", `{"model":"gpt-image-2","resolution":"2k"}`, "channel_73_only_supports_1k", 59},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := imageDiagnosticContext(tc.body)
			PrepareGptImage2ModelRequest(c, "gpt-image-2")
			ch, err := SelectCheapestEnabledChannel(c, "gpt-image-2")
			require.NoError(t, err)
			require.Equal(t, tc.selected, ch.Id)
			trace := imageRoutingTrace(c)
			foundPrice, foundReason := false, false
			for _, event := range trace.Events {
				if event["channel_id"] != 73 {
					continue
				}
				if event["stage"] == "price" {
					require.InDelta(t, 0.0075, event["routing_base_price_usd"], 1e-9)
					foundPrice = true
				}
				if event["stage"] == "capability" {
					require.Equal(t, tc.reason, event["reason"])
					foundReason = true
				}
			}
			require.True(t, foundPrice)
			require.True(t, foundReason)
			require.Equal(t, "cheapest_compatible", trace.Events[len(trace.Events)-1]["reason"])
		})
	}
	t.Run("cached choice is preserved and identified", func(t *testing.T) {
		require.NoError(t, getChannelRoutingCache().SetWithTTL("gpt-image-2:asc", 59, channelRoutingCacheTTL))
		c := imageDiagnosticContext(`{"model":"gpt-image-2","n":1,"size":"1024x1024"}`)
		PrepareGptImage2ModelRequest(c, "gpt-image-2")
		ch, err := SelectCheapestEnabledChannel(c, "gpt-image-2")
		require.NoError(t, err)
		require.Equal(t, 59, ch.Id)
		found := false
		for _, event := range imageRoutingTrace(c).Events {
			if event["stage"] == "cache_hit" {
				require.Equal(t, 59, event["channel_id"])
				require.Equal(t, 73, event["current_cheapest_channel_id"])
				found = true
			}
		}
		require.True(t, found)
	})
}
