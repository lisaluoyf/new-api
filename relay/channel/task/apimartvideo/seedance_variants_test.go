package apimartvideo

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
	"io"
	"testing"
)

func TestSeedanceVariantsRejectUnsupportedSpecifications(t *testing.T) {
	for _, name := range []string{ModelSeedance20Fast, ModelSeedance20Mini} {
		for _, extra := range []string{`"resolution":"1080p"`, `"resolution":"4k"`, `"duration":3`, `"duration":16`, `"duration":-1`} {
			c := generationContext("/v1/videos/generations", fmt.Sprintf(`{"model":%q,"prompt":"test",%s}`, name, extra))
			e := (&TaskAdaptor{}).ValidateRequestAndSetAction(c, &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}})
			require.NotNil(t, e)
			require.Equal(t, 400, e.StatusCode)
			require.True(t, e.LocalError)
		}
	}
}
func TestSeedanceVariantsDefaultsAndReferenceBilling(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	old, exists := common.OptionMap["VideoModelPricing"]
	common.OptionMapRWMutex.Unlock()
	defer func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if exists {
			common.OptionMap["VideoModelPricing"] = old
		} else {
			delete(common.OptionMap, "VideoModelPricing")
		}
	}()
	for _, name := range []string{ModelSeedance20Fast, ModelSeedance20Mini} {
		common.OptionMapRWMutex.Lock()
		common.OptionMap["VideoModelPricing"] = fmt.Sprintf(`{%q:{"unit":"second","base_price":0.1,"base_variant":"720P","prices":{"480P":0.05,"480P-input":0.03,"720P":0.1,"720P-input":0.06}}}`, name)
		common.OptionMapRWMutex.Unlock()
		for _, hasVideo := range []bool{false, true} {
			extra := ""
			if hasVideo {
				extra = `,"video_urls":["https://example.com/source.mp4"]`
			}
			c := generationContext("/v1/videos/generations", fmt.Sprintf(`{"model":%q,"prompt":"test","resolution":"480p"%s}`, name, extra))
			c.Set("seedance_video_input_seconds", 0)
			if hasVideo {
				c.Set("seedance_video_input_seconds", 7)
			}
			a := &TaskAdaptor{}
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: name}, TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
			require.Nil(t, a.ValidateRequestAndSetAction(c, info))
			reader, e := a.BuildRequestBody(c, info)
			require.NoError(t, e)
			raw, e := io.ReadAll(reader)
			require.NoError(t, e)
			var f map[string]any
			require.NoError(t, common.Unmarshal(raw, &f))
			require.Equal(t, float64(5), f["duration"])
			require.Equal(t, true, f["generate_audio"])
			ratios := a.EstimateBilling(c, info)
			if hasVideo {
				require.Equal(t, 12.0, ratios["seconds"])
				require.InDelta(t, .3, ratios["size"], 1e-9)
			} else {
				require.Equal(t, 5.0, ratios["seconds"])
				require.InDelta(t, .5, ratios["size"], 1e-9)
			}
		}
	}
}
func TestVariantSettlementUsesFrozenTariffAndMeasuredInput(t *testing.T) {
	for _, name := range []string{ModelSeedance20Fast, ModelSeedance20Mini} {
		task := &model.Task{Properties: model.Properties{OriginModelName: name}, Data: []byte(`{"data":{"duration":6,"cost":0.00001}}`), PrivateData: model.TaskPrivateData{SeedanceRequest: map[string]any{"duration": 5, "video_input_seconds": 7}, BillingContext: &model.TaskBillingContext{ModelPrice: .11, GroupRatio: 1.05, OtherRatios: map[string]float64{"seconds": 12, "size": .3}}}}
		require.Equal(t, int(13*.11*.3*1.05*common.QuotaPerUnit+.5), (&TaskAdaptor{}).AdjustBillingOnComplete(task, &relaycommon.TaskInfo{}))
	}
}

func TestSeedanceVariantsRejectConflictingMaterialsBeforeBilling(t *testing.T) {
	for _, name := range []string{ModelSeedance20Fast, ModelSeedance20Mini} {
		for _, extra := range []string{
			`"audio_urls":["https://example.com/ref.mp3"]`,
			`"image_urls":[],"image_with_roles":[]`,
			`"image_with_roles":[{"url":"https://example.com/a.png","role":"last_frame"}]`,
			`"image_with_roles":[{"url":"https://example.com/a.png","role":"first_frame"}],"aspect_ratio":"16:9"`,
			`"aspect_ratio":"adaptive","image_with_roles":[{"url":"https://example.com/a.png","role":"first_frame"}],"video_urls":["https://example.com/a.mp4"]`,
			`"audio":false,"generate_audio":true`,
		} {
			c := generationContext("/v1/videos/generations", fmt.Sprintf(`{"model":%q,"prompt":"scene",%s}`, name, extra))
			e := (&TaskAdaptor{}).ValidateRequestAndSetAction(c, &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}})
			require.NotNil(t, e, extra)
			require.Equal(t, 400, e.StatusCode)
		}
	}
}
