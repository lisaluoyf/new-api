package apimartomni

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fixture(t *testing.T, upstream, extra string) (*TaskAdaptor, *gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/videos/generations", strings.NewReader(`{"model":"`+Model+`","prompt":"a paper boat on a lake"`+extra+`}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("model_mapping", `{"`+Model+`":"`+upstream+`"}`)
	info := &relaycommon.RelayInfo{OriginModelName: Model, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://api.apimart.ai"}, TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	a := &TaskAdaptor{}
	a.Init(info)
	return a, c, info
}
func TestOmniPayloads(t *testing.T) {
	for _, tc := range []struct {
		upstream, extra string
		seconds, price  float64
	}{
		{Model, ``, 10, .88}, {Model, `,"resolution":"4K","first_frame_image":"https://example.com/start.png","last_frame_image":"https://example.com/end.png","metadata":{"task":"image_to_video"}`, 10, 2.64},
		{Ext, `,"duration":4,"resolution":"360p","nsfw_check":false`, 4, .15}, {Ext, `,"duration":10,"resolution":"4k"`, 10, .9}, {Ext, ``, 6, .3},
	} {
		t.Run(tc.upstream+tc.extra, func(t *testing.T) {
			a, c, i := fixture(t, tc.upstream, tc.extra)
			require.Nil(t, a.ValidateRequestAndSetAction(c, i))
			r, e := a.BuildRequestBody(c, i)
			require.NoError(t, e)
			var f map[string]any
			require.NoError(t, common.DecodeJson(r, &f))
			require.Equal(t, tc.upstream, f["model"])
			if tc.upstream == Model {
				require.NotContains(t, f, "duration")
			} else {
				require.Equal(t, tc.seconds, f["duration"])
			}
			if tc.extra == `,"duration":4,"resolution":"360p","nsfw_check":false` {
				require.Equal(t, false, f["nsfw_check"])
			}
			ratios := a.EstimateBilling(c, i)
			base := OfficialGoldBase
			if tc.upstream == Ext {
				base = ExtGoldBase
			}
			require.InDelta(t, tc.price, base*ratios["seconds"]*ratios["size"], 1e-9)
		})
	}
}
func TestOmniInvalidBeforeBilling(t *testing.T) {
	for _, extra := range []string{`,"duration":0`, `,"duration":5`, `,"duration":4.5`, `,"resolution":"8k"`, `,"image_urls":["file:///etc/passwd"]`, `,"image_urls":["https://example.com/a","https://example.com/b"]`, `,"duration":4,"video_urls":["https://example.com/v.mp4"]`, `,"aspect_ratio":"1:1"`, `,"aspect_ratio":"16:9","size":"9:16"`, `,"nsfw_check":0`, `,"unexpected":true`} {
		a, c, i := fixture(t, Ext, extra)
		require.NotNil(t, a.ValidateRequestAndSetAction(c, i), extra)
		_, ok := c.Get("task_request")
		require.False(t, ok)
	}
	a, c, i := fixture(t, Model, `,"duration":6`)
	require.Equal(t, 503, a.ValidateRequestAndSetAction(c, i).StatusCode)
	a, c, i = fixture(t, Ext, `,"first_frame_image":"https://example.com/a"`)
	require.Equal(t, 503, a.ValidateRequestAndSetAction(c, i).StatusCode)
	a, c, i = fixture(t, Model, `,"last_frame_image":"https://example.com/a"`)
	require.NotNil(t, a.ValidateRequestAndSetAction(c, i))
}
func TestOmniGoldenTariffs(t *testing.T) {
	for _, res := range []string{"360p", "720p", "1080p", "4k"} {
		for _, seconds := range []int{4, 6, 8, 10} {
			require.Positive(t, GoldenPriceUSD(res, seconds, true, false))
			require.Positive(t, GoldenPriceUSD(res, seconds, false, false))
		}
	}
	require.InDelta(t, .175, GoldenPriceUSD("360p", 6, true, false), 1e-12)
	require.InDelta(t, .75, GoldenPriceUSD("4k", 4, true, false), 1e-12)
	require.InDelta(t, .8, GoldenPriceUSD("720p", 10, true, true), 1e-12)
}
func TestOmniActualSettlement(t *testing.T) {
	for _, up := range []string{Model, Ext} {
		base := OfficialGoldBase
		if up == Ext {
			base = ExtGoldBase
		}
		for _, cost := range []float64{.15, .3, .9, 1.423456} {
			task := &model.Task{Quota: 500000, Properties: model.Properties{UpstreamModelName: up}, PrivateData: model.TaskPrivateData{BillingContext: &model.TaskBillingContext{ModelPrice: base * 1.1, GroupRatio: 1, OtherRatios: map[string]float64{"seconds": 10, "size": 1}}}}
			task.Data = []byte(`{"data":{"cost":` + fmtFloat(cost) + `}}`)
			a := &TaskAdaptor{}
			got := a.AdjustBillingOnComplete(task, &relaycommon.TaskInfo{Status: model.TaskStatusSuccess})
			require.Equal(t, int(math.Round(cost*1.1*common.QuotaPerUnit)), got)
			require.GreaterOrEqual(t, float64(got)/common.QuotaPerUnit, cost)
		}
	}
}
func fmtFloat(n float64) string { b, _ := common.Marshal(n); return string(b) }
func TestOmniTaskTransport(t *testing.T) {
	a, c, i := fixture(t, Model, ``)
	i.PublicTaskID = "task_public"
	id, _, e := a.DoResponse(c, &http.Response{Body: io.NopCloser(strings.NewReader(`{"code":200,"data":[{"task_id":"upstream_secret"}]}`))}, i)
	require.Nil(t, e)
	require.Equal(t, "upstream_secret", id)
	result, err := a.ParseTaskResult([]byte(`{"code":200,"data":{"status":"completed","result":{"videos":[{"url":["https://example.com/video.mp4"]}]}}}`))
	require.NoError(t, err)
	require.Equal(t, model.TaskStatusSuccess, result.Status)
	require.Equal(t, "https://example.com/video.mp4", result.Url)
	_, err = a.ParseTaskResult([]byte(`{"code":200,"data":{"status":"completed"}}`))
	require.Error(t, err)
	result, err = a.ParseTaskResult([]byte(`{"code":200,"data":{"status":"failed","error":{"message":"rejected"}}}`))
	require.NoError(t, err)
	require.Equal(t, model.TaskStatusFailure, result.Status)
	require.False(t, IsChannel("https://api.apimart.ai.attacker.example"))
}
