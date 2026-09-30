package apimartvideo

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func generationContext(path, body string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c
}

func TestGenerationAliasesPreserveSeedanceParametersAndPricing(t *testing.T) {
	for _, model := range []string{ModelSeedance20, ModelSeedance25} {
		for _, res := range []string{"480p", "720p", "1080p"} {
			var expected map[string]interface{}
			var expectedRatios map[string]float64
			for _, path := range []string{"/v1/video/generations", "/v1/videos/generations"} {
				raw := common.MapToJsonStr(map[string]interface{}{"model": model, "prompt": "test", "duration": 4, "resolution": res, "ratio": "9:16", "image_urls": []string{"https://ref.example/first.png"}, "video_urls": []string{"https://ref.example/input.mp4"}, "audio_urls": []string{"https://ref.example/sound.mp3"}, "generate_audio": false, "watermark": false})
				c := generationContext(path, raw)
				info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "mapped-seedance"}, TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
				a := &TaskAdaptor{}
				require.Nil(t, a.ValidateRequestAndSetAction(c, info), path)
				reader, err := a.BuildRequestBody(c, info)
				require.NoError(t, err)
				encoded, err := io.ReadAll(reader)
				require.NoError(t, err)
				var body map[string]interface{}
				require.NoError(t, common.Unmarshal(encoded, &body))
				require.Equal(t, res, body["resolution"])
				require.Equal(t, "9:16", body["aspect_ratio"])
				require.Equal(t, "mapped-seedance", body["model"])
				require.NotContains(t, body, "ratio")
				require.Equal(t, false, body["generate_audio"])
				require.Equal(t, false, body["watermark"])
				for _, field := range []string{"image_urls", "video_urls", "audio_urls"} {
					require.Len(t, body[field], 1)
				}
				ratios := a.EstimateBilling(c, info)
				require.Equal(t, 4.0, ratios["seconds"])
				if expected == nil {
					expected = body
					expectedRatios = ratios
				} else {
					require.Equal(t, expected, body)
					require.Equal(t, expectedRatios, ratios)
				}
				req, err := relaycommon.GetTaskRequest(c)
				require.NoError(t, err)
				require.Equal(t, res+"-input", req.Metadata["billing_variant"])
			}
		}
	}
}

func TestGenerationRejectsInvalidAndConflictingParameters(t *testing.T) {
	for _, extra := range []string{
		`"resolution":"2k"`, `"resolution":"4k"`, `"resolution":480`, `"resolution":null`, `"resolution":""`,
		`"resolution":"480p","size":"720P"`,
		`"ratio":"9:16","aspect_ratio":"16:9"`,
		`"ratio":"nonsense"`, `"duration":3`, `"duration":31`,
		`"duration":4,"seconds":"5"`, `"duration":4.5`, `"size":"garbage"`,
	} {
		for _, path := range []string{"/v1/video/generations", "/v1/videos/generations"} {
			c := generationContext(path, `{"model":"seedance-2.5","prompt":"test",`+extra+`}`)
			e := (&TaskAdaptor{}).ValidateRequestAndSetAction(c, &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}})
			require.NotNil(t, e, extra)
			require.Equal(t, 400, e.StatusCode, extra)
			require.True(t, e.LocalError)
		}
	}
}

func TestGenerationSupportsLegacySizeAndMetadata(t *testing.T) {
	for _, raw := range []string{
		`{"model":"seedance-2.5","prompt":"test","seconds":"4","size":"1080x1920","input_reference":"https://ref.example/image.png"}`,
		`{"model":"seedance-2.5","prompt":"test","duration":4,"resolution":"1080p","size":"9:16","images":["https://ref.example/image.png"]}`,
		`{"model":"seedance-2.5","prompt":"test","duration":"4","metadata":{"resolution":"1080p","aspect_ratio":"9:16"},"image":"https://ref.example/image.png"}`,
	} {
		c := generationContext("/v1/video/generations", raw)
		a := &TaskAdaptor{}
		require.Nil(t, a.ValidateRequestAndSetAction(c, &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}))
		req, err := relaycommon.GetTaskRequest(c)
		require.NoError(t, err)
		require.Equal(t, "1080p", req.Metadata["resolution"])
		require.Equal(t, "9:16", req.Metadata["aspect_ratio"])
		require.Equal(t, 4, req.Duration)
		require.Len(t, req.Images, 1)
	}
}
