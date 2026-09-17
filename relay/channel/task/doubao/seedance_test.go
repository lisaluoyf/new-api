package doubao

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func seedanceContext(body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos/generations", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, w
}

func TestSeedancePublicRequestMappingAndBilling(t *testing.T) {
	for public, upstream := range map[string]string{"doubao-seedance-2.0": "tencent-seedance-1-0-pro", "seedance-2.5": "tencent-seedance-1-5-pro"} {
		t.Run(public, func(t *testing.T) {
			c, _ := seedanceContext(`{"model":"` + public + `","prompt":"A teapot","duration":5,"resolution":"1080p","aspect_ratio":"9:16","generate_audio":false,"watermark":false,"seed":0,"image_urls":["https://example.com/reference.png"]}`)
			info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}, OriginModelName: public, ChannelMeta: &relaycommon.ChannelMeta{IsModelMapped: true, UpstreamModelName: upstream}}
			a := &TaskAdaptor{}
			require.Nil(t, a.ValidateRequestAndSetAction(c, info))
			body, err := a.BuildRequestBody(c, info)
			require.NoError(t, err)
			data, err := io.ReadAll(body)
			require.NoError(t, err)
			var payload map[string]interface{}
			require.NoError(t, common.Unmarshal(data, &payload))
			require.Equal(t, upstream, payload["model"])
			require.Equal(t, float64(5), payload["duration"])
			require.Equal(t, "1080p", payload["resolution"])
			require.Equal(t, "9:16", payload["ratio"])
			require.Equal(t, false, payload["generate_audio"])
			require.Equal(t, false, payload["watermark"])
			require.Equal(t, float64(0), payload["seed"])
			require.Len(t, payload["content"], 2)
			ratios := a.EstimateBilling(c, info)
			require.Equal(t, float64(5), ratios["seconds"])
			require.Greater(t, ratios["size"], float64(1))
		})
	}
}

func TestSeedanceDefaultsMetadataAndValidation(t *testing.T) {
	for _, tc := range []struct {
		body    string
		seconds float64
		valid   bool
	}{
		{`{"model":"seedance-2.5","prompt":"scene"}`, 5, true},
		{`{"model":"seedance-2.5","prompt":"scene","seconds":"10","metadata":{"duration":5,"resolution":"720p"}}`, 10, true},
		{`{"model":"seedance-2.5","prompt":"scene","duration":-1}`, 30, true},
		{`{"model":"seedance-2.5","prompt":"scene","duration":0}`, 0, false},
		{`{"model":"seedance-2.5","prompt":"scene","duration":31}`, 0, false},
		{`{"model":"seedance-2.5","prompt":"scene","resolution":"8K"}`, 0, false},
		{`{"model":"seedance-2.5","prompt":"scene","image_urls":123}`, 0, false},
		{`{"model":"seedance-2.5","prompt":"scene","generate_audio":{}}`, 0, false},
	} {
		c, _ := seedanceContext(tc.body)
		a := &TaskAdaptor{}
		err := a.ValidateRequestAndSetAction(c, &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}})
		if !tc.valid {
			require.NotNil(t, err, tc.body)
			require.Equal(t, 400, err.StatusCode)
			continue
		}
		require.Nil(t, err, tc.body)
		require.Equal(t, tc.seconds, a.EstimateBilling(c, &relaycommon.RelayInfo{})["seconds"])
	}
}

func TestSeedanceSubmitPollAndActualDuration(t *testing.T) {
	a := &TaskAdaptor{}
	c, w := seedanceContext(`{}`)
	info := &relaycommon.RelayInfo{OriginModelName: "seedance-2.5", TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	info.PublicTaskID = "task_public"
	id, _, taskErr := a.DoResponse(c, &http.Response{Body: io.NopCloser(strings.NewReader(`{"id":"provider_task","status":"queued"}`))}, info)
	require.Nil(t, taskErr)
	require.Equal(t, "provider_task", id)
	require.JSONEq(t, `{"code":200,"data":[{"status":"submitted","task_id":"task_public"}]}`, w.Body.String())
	result, err := a.ParseTaskResult([]byte(`{"status":"succeeded","duration":5,"content":{"video_url":"https://example.com/video.mp4"}}`))
	require.NoError(t, err)
	require.Equal(t, model.TaskStatusSuccess, result.Status)
	require.Equal(t, 5, result.BillableSeconds)
	task := &model.Task{TaskID: "task_public", Quota: 30000, Properties: model.Properties{OriginModelName: "seedance-2.5"}, PrivateData: model.TaskPrivateData{BillingContext: &model.TaskBillingContext{OtherRatios: map[string]float64{"seconds": 30}}}, Data: []byte(`{"status":"succeeded","content":{"video_url":"https://example.com/provider.mp4"}}`)}
	require.Equal(t, 5000, a.AdjustBillingOnComplete(task, result))
	public, err := a.ConvertToOpenAIVideo(task)
	require.NoError(t, err)
	require.Contains(t, string(public), "/v1/videos/task_public/content")
	require.NotContains(t, string(public), "provider.mp4")
	failed, err := a.ParseTaskResult([]byte(`{"status":"failed","error":{"message":"generation rejected"}}`))
	require.NoError(t, err)
	require.Equal(t, model.TaskStatusFailure, failed.Status)
}

func TestNativeDoubaoRequestUnchanged(t *testing.T) {
	c, _ := seedanceContext(`{"model":"doubao-seedance-2-0-260128","prompt":"scene","seconds":"5","metadata":{"resolution":"1080p","generate_audio":false}}`)
	a := &TaskAdaptor{}
	require.Nil(t, a.ValidateRequestAndSetAction(c, &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}))
	require.Nil(t, a.EstimateBilling(c, &relaycommon.RelayInfo{}))
	body, err := a.BuildRequestBody(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}})
	require.NoError(t, err)
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Contains(t, string(data), `"duration":5`)
	require.Contains(t, string(data), `"generate_audio":false`)
}
