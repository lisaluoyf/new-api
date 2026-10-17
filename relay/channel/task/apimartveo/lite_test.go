package apimartveo

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestLitePayloadAndResolutionBilling(t *testing.T) {
	for _, upstream := range []string{"veo3.1-lite", "veo3.1-lite-ext"} {
		for _, tc := range []struct {
			extra string
			ratio float64
		}{{``, 1}, {`,"seconds":"8","size":"1080p","aspect_ratio":"9:16"`, 1.6}} {
			a, c, info := fixture(t, Lite, upstream, tc.extra)
			require.Nil(t, a.ValidateRequestAndSetAction(c, info))
			body, err := a.BuildRequestBody(c, info)
			require.NoError(t, err)
			var payload map[string]any
			require.NoError(t, common.DecodeJson(body, &payload))
			require.Equal(t, upstream, payload["model"])
			for _, key := range []string{"generate_audio", "image_urls", "generation_type", "official_fallback"} {
				require.NotContains(t, payload, key)
			}
			require.InDelta(t, tc.ratio, a.EstimateBilling(c, info)["size"], 1e-9)
			require.Equal(t, float64(8), a.EstimateBilling(c, info)["seconds"])
		}
	}
	require.True(t, IsModel(Lite))
	require.Equal(t, .05, OfficialPrice(Lite, "720p", true))
	require.Equal(t, .08, OfficialPrice(Lite, "1080p", true))
	require.Zero(t, OfficialPrice(Lite, "4k", true))
}

func TestLiteUnsupportedCapabilitiesNeverReachBilling(t *testing.T) {
	for _, extra := range []string{`,"duration":4`, `,"seconds":"6"`, `,"generate_audio":false`, `,"resolution":"4k"`, `,"size":"4K"`, `,"image_urls":[]`, `,"input_reference":"https://example.com/a.png"`, `,"first_frame_image":"https://example.com/a.png"`, `,"generation_type":"frame"`, `,"seed":0`, `,"negative_prompt":"blur"`, `,"official_fallback":false`, `,"metadata":{"image_urls":["https://example.com/a.png"]}`} {
		t.Run(extra, func(t *testing.T) {
			a, c, info := fixture(t, Lite, "veo3.1-lite", extra)
			e := a.ValidateRequestAndSetAction(c, info)
			require.NotNil(t, e)
			require.Equal(t, 400, e.StatusCode)
			_, exists := c.Get("task_request")
			require.False(t, exists)
			_, exists = c.Get("veo_request")
			require.False(t, exists)
		})
	}
	a, c, info := fixture(t, Lite, "veo3.1-lite", ``)
	info.Action = constant.TaskActionRemix
	require.Equal(t, 400, a.ValidateRequestAndSetAction(c, info).StatusCode)
	a, c, info = fixture(t, Lite, "veo3.1-lite-official", ``)
	require.Equal(t, 400, a.ValidateRequestAndSetAction(c, info).StatusCode)
}
