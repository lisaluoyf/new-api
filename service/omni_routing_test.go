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

func TestOmniRequestCapabilityFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mapping := `{"gemini-omni-1.1-flash":"gemini-omni-1.1-flash-ext"}`
	official := &model.Channel{Id: 318}
	ext := &model.Channel{Id: 319, ModelMapping: &mapping}
	for _, tc := range []struct {
		name, body    string
		official, ext bool
	}{
		{"plain", `{"model":"gemini-omni-1.1-flash","prompt":"test"}`, true, true},
		{"duration", `{"duration":6}`, false, true},
		{"seconds alias", `{"seconds":4}`, false, true},
		{"first frame", `{"first_frame_image":"https://example.com/a.png"}`, true, false},
		{"first last", `{"first_frame_image":"https://example.com/a.png","last_frame_image":"https://example.com/b.png"}`, true, false},
		{"edit", `{"metadata":{"task":"edit"},"video_urls":["https://example.com/a.mp4"]}`, true, false},
		{"3 references", `{"generation_type":"reference","image_urls":["https://example.com/a.png","https://example.com/b.png","https://example.com/c.png"]}`, false, true},
		{"2 images", `{"image_urls":["https://example.com/a.png","https://example.com/b.png"]}`, true, false},
		{"conflicting", `{"duration":6,"first_frame_image":"https://example.com/a.png"}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/videos", strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			filter := OmniChannelPickFilter(c, omniRoutingModel)
			require.NotNil(t, filter)
			require.Equal(t, tc.official, filter(official))
			require.Equal(t, tc.ext, filter(ext))
			require.Equal(t, tc.official, OmniChannelMatchesRequest(c, official, omniRoutingModel))
			var preserved map[string]any
			require.NoError(t, common.UnmarshalBodyReusable(c, &preserved))
			require.NotNil(t, preserved)
		})
	}
	require.Nil(t, OmniChannelPickFilter(nil, "other"))
}
func TestOmniContinuationFilterPinsParentChannel(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/videos", strings.NewReader(`{}`))
	c.Set("omni_routing_requirement", omniRoutingRequirement{official: true, parentChannel: 318})
	filter := OmniChannelPickFilter(c, omniRoutingModel)
	require.True(t, filter(&model.Channel{Id: 318}))
	require.False(t, filter(&model.Channel{Id: 320}))
}
