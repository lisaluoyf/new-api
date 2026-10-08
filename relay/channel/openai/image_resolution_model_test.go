package openai

import (
	"bytes"
	"encoding/base64"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestImageResolutionModelMappingOnJSONAndNativeEdits(t *testing.T) {
	const original = "gemini-3.1-flash-image"
	const preview = original + "-preview"
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/l9sAAAAASUVORK5CYII=")
	require.NoError(t, err)
	old := subrouterGeminiDownload
	t.Cleanup(func() { subrouterGeminiDownload = old })
	subrouterGeminiDownload = func(string, ...string) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(png))}, nil
	}
	for _, references := range []bool{false, true} {
		for _, tc := range []struct{ resolution, want, size string }{{"1K", original, "1024x1024"}, {"2k", original, "2048x2048"}, {" 4K ", preview, "4096x4096"}, {"", original, "1024x1024"}} {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/images/generations/async", nil)
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set("model_mapping", `{"gemini-3.1-flash-image":"gemini-3.1-flash-image-preview"}`)
			info := &relaycommon.RelayInfo{OriginModelName: original, RelayMode: relayconstant.RelayModeImagesGenerations, ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "https://subrouter.ai", UpstreamModelName: original, ChannelSetting: dto.ChannelSettings{ImageResolutionModelMapping: map[string]map[string]string{original: {"1k": original, "2k": original, "4k": preview}}}}}
			request := dto.ImageRequest{Model: original, Size: "1:1", Resolution: tc.resolution}
			if references {
				request.ImageUrls = []string{"https://example.com/ref.png"}
			}
			require.NoError(t, helper.ModelMappedHelper(c, info, &request))
			helper.ImageResolutionModelMappedHelper(info, &request)
			wire, err := (&Adaptor{}).ConvertImageRequest(c, info, request)
			require.NoError(t, err)
			if references {
				req := httptest.NewRequest("POST", "/v1/images/edits", wire.(*bytes.Buffer))
				req.Header.Set("Content-Type", c.GetString("subrouter_gemini_edit_content_type"))
				require.NoError(t, req.ParseMultipartForm(1<<20))
				require.Equal(t, tc.want, req.FormValue("model"))
				require.Equal(t, tc.size, req.FormValue("size"))
				require.Equal(t, tc.resolution, req.FormValue("resolution"))
				req.MultipartForm.RemoveAll()
			} else {
				raw, err := common.Marshal(wire)
				require.NoError(t, err)
				var sent dto.ImageRequest
				require.NoError(t, common.Unmarshal(raw, &sent))
				require.Equal(t, tc.want, sent.Model)
				require.Equal(t, tc.size, sent.Size)
			}
			require.Equal(t, tc.want, info.UpstreamModelName)
			require.Equal(t, original, info.OriginModelName)
			require.Equal(t, tc.resolution, request.Resolution)
		}
	}
	for _, tc := range []struct {
		origin string
		rules  map[string]map[string]string
	}{{original, nil}, {"gemini-3-pro-image", map[string]map[string]string{original: {"4k": preview}}}} {
		info := &relaycommon.RelayInfo{OriginModelName: tc.origin, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "static-target", ChannelSetting: dto.ChannelSettings{ImageResolutionModelMapping: tc.rules}}}
		request := dto.ImageRequest{Model: "static-target", Resolution: "4K"}
		helper.ImageResolutionModelMappedHelper(info, &request)
		require.Equal(t, "static-target", request.Model)
		require.Equal(t, "static-target", info.UpstreamModelName)
	}
}
