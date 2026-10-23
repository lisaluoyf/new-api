package apimartklingv4

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestKlingV4PayloadPricingAndPublicResult(t *testing.T) {
	old := common.OptionMap
	t.Cleanup(func() { common.OptionMap = old })
	common.OptionMap = map[string]string{"VideoModelPricing": `{"kling-v4":{"prices":{"720P":0.2,"1080P":0.3,"720P-input":0.1,"1080P-input":0.15}}}`}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/videos/generations", strings.NewReader(`{"model":"kling-v4","prompt":"Animate the frames","image_with_roles":[{"url":"https://example.com/start.png","role":"first_frame"},{"url":"https://example.com/mid.png","role":"keyframe"}],"duration":3,"resolution":"1080p","watermark":false}`))
	c.Request.Header.Set("Content-Type", "application/json")
	a := &TaskAdaptor{}
	info := &relaycommon.RelayInfo{OriginModelName: "kling-v4", TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	require.Nil(t, a.ValidateRequestAndSetAction(c, info))
	r := a.EstimateBilling(c, info)
	require.Equal(t, float64(3), r["seconds"])
	require.InDelta(t, 1.5, r["size"], 1e-10)
	body, e := a.BuildRequestBody(c, info)
	require.NoError(t, e)
	raw, e := io.ReadAll(body)
	require.NoError(t, e)
	require.Contains(t, string(raw), `"role":"keyframe"`)
	require.Contains(t, string(raw), `"watermark":false`)
	require.NotContains(t, string(raw), "aspect_ratio")
	result, e := a.ParseTaskResult([]byte(`{"code":200,"data":{"status":"completed","result":{"videos":[{"url":["https://example.com/private.mp4"]}]}}}`))
	require.NoError(t, e)
	require.Equal(t, string(model.TaskStatusSuccess), result.Status)
	public, e := a.ConvertToOpenAIVideo(&model.Task{TaskID: "task_public", Status: model.TaskStatus(result.Status), Progress: "100%"})
	require.NoError(t, e)
	require.Contains(t, string(public), "task_public")
	require.NotContains(t, string(public), "private.mp4")
}
func TestKlingV4UnconfiguredPriceFailsClosed(t *testing.T) {
	old := common.OptionMap
	t.Cleanup(func() { common.OptionMap = old })
	common.OptionMap = map[string]string{"VideoModelPricing": `{"kling-v4":{"prices":{}}}`}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/videos/generations", strings.NewReader(`{"model":"kling-v4","prompt":"test"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	require.NotNil(t, (&TaskAdaptor{}).ValidateRequestAndSetAction(c, &relaycommon.RelayInfo{}))
}

func TestKlingV4OfficialBalanceBaseline(t *testing.T) {
	old := common.OptionMap
	t.Cleanup(func() { common.OptionMap = old })
	common.OptionMap = map[string]string{}
	for variant, want := range map[string]float64{"720p": .157, "1080p": .235, "720p-input": .094, "1080p-input": .141} {
		require.Equal(t, want, Price(variant))
	}
	require.Zero(t, Price("4k"))
}

func TestKlingV4ImageRequiresPromptBeforeCharge(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/videos/generations", strings.NewReader(`{"model":"kling-v4","image_urls":["https://example.com/start.png"]}`))
	c.Request.Header.Set("Content-Type", "application/json")
	require.NotNil(t, (&TaskAdaptor{}).ValidateRequestAndSetAction(c, &relaycommon.RelayInfo{}))
}
