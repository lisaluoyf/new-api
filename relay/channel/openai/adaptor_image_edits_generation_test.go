package openai

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestImageEditsGenerationAdaptorSendsJSON(t *testing.T) {
	service.InitHttpClient()
	for _, id := range []int{59, 81} {
		var received dto.ImageRequest
		var receivedPath, receivedType string
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedPath = r.URL.Path
			receivedType = r.Header.Get("Content-Type")
			raw, _ := io.ReadAll(r.Body)
			_ = common.Unmarshal(raw, &received)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[{"url":"https://example.test/result.png"}]}`)
		}))
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/images/edits", nil)
		c.Request.Header.Set("Content-Type", "application/json")
		format := dto.GptImage2SizeFormatAspectRatioWithResolution
		if id == 149 {
			format = dto.GptImage2SizeFormatPixelDimensions
		}
		info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesEdits, OriginModelName: "gpt-image-2", RequestURLPath: "/v1/images/edits", ChannelMeta: &relaycommon.ChannelMeta{ChannelId: id, ChannelType: constant.ChannelTypeOpenAI, ChannelBaseUrl: upstream.URL, ChannelOtherSettings: dto.ChannelOtherSettings{GptImage2Capabilities: &dto.GptImage2Capabilities{SizeFormat: format}}}}
		a := &Adaptor{}
		a.Init(info)
		var req dto.ImageRequest
		require.NoError(t, common.Unmarshal([]byte(`{"model":"gpt-image-2-official","prompt":"edit","image":"data:image/png;base64,aGVsbG8=","resolution":"2k","size":"1:1"}`), &req))
		converted, err := a.ConvertImageRequest(c, info, req)
		require.NoError(t, err)
		raw, err := common.Marshal(converted)
		require.NoError(t, err)
		resp, err := a.DoRequest(c, info, bytes.NewReader(raw))
		require.NoError(t, err)
		resp.(*http.Response).Body.Close()
		upstream.Close()
		require.Equal(t, "/v1/images/generations", receivedPath)
		require.Equal(t, "application/json", receivedType)
		require.Equal(t, "gpt-image-2-official", received.Model)
		require.Equal(t, []string{"data:image/png;base64,aGVsbG8="}, received.ImageUrls)
		if id == 149 {
			require.Equal(t, "2048x2048", received.Size)
			require.Empty(t, received.Resolution)
		} else {
			require.Equal(t, "1:1", received.Size)
			require.Equal(t, "2k", received.Resolution)
		}
		require.Equal(t, relayconstant.RelayModeImagesEdits, info.RelayMode)
		require.Equal(t, "/v1/images/edits", info.RequestURLPath)
		info.ChannelId = 102
		url, err := a.GetRequestURL(info)
		require.NoError(t, err)
		require.Equal(t, upstream.URL+"/v1/images/edits", url)
	}
}

func TestImage25MultipartEditsGenerationKeepsReferencesAndNativeFallback(t *testing.T) {
	for _, id := range []int{59, 81} {
		for _, name := range []string{"gpt-image-2.5-flare", "gpt-image-2.5-sunburst"} {
			for _, path := range []string{"/v1/images/edits", "/v1/images/edits/async"} {
				var body bytes.Buffer
				writer := multipart.NewWriter(&body)
				for k, v := range map[string]string{"model": name, "prompt": "preserve the reference", "n": "1", "size": "9:16", "resolution": "1k"} {
					require.NoError(t, writer.WriteField(k, v))
				}
				for _, key := range []string{"image[]", "image[1]"} {
					part, err := writer.CreateFormFile(key, "reference.png")
					require.NoError(t, err)
					_, err = part.Write([]byte("unique reference bytes"))
					require.NoError(t, err)
				}
				require.NoError(t, writer.Close())
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest("POST", path, bytes.NewReader(body.Bytes()))
				c.Request.Header.Set("Content-Type", writer.FormDataContentType())
				t.Cleanup(func() { common.CleanupBodyStorage(c) })
				mapped := name
				if id == 81 {
					mapped = "gpt-image-2.5-ext"
				}
				info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesEdits, OriginModelName: name, RequestURLPath: path, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: id, ChannelType: constant.ChannelTypeOpenAI, ChannelBaseUrl: "https://example.test", ChannelOtherSettings: dto.ChannelOtherSettings{GptImage2Capabilities: &dto.GptImage2Capabilities{SizeFormat: dto.GptImage2SizeFormatAspectRatioWithResolution}}}}
				a := &Adaptor{}
				a.Init(info)
				converted, err := a.ConvertImageRequest(c, info, dto.ImageRequest{Model: mapped})
				require.NoError(t, err)
				raw, err := common.Marshal(converted)
				require.NoError(t, err)
				var got dto.ImageRequest
				require.NoError(t, common.Unmarshal(raw, &got))
				require.Equal(t, mapped, got.Model)
				require.Equal(t, "preserve the reference", got.Prompt)
				require.Equal(t, []string{"data:image/png;base64,dW5pcXVlIHJlZmVyZW5jZSBieXRlcw==", "data:image/png;base64,dW5pcXVlIHJlZmVyZW5jZSBieXRlcw=="}, got.ImageUrls)
				url, err := a.GetRequestURL(info)
				require.NoError(t, err)
				require.Equal(t, "https://example.test/v1/images/generations", url)
				require.Equal(t, path, c.Request.URL.Path)
				require.Equal(t, path, info.RequestURLPath)
				require.Equal(t, relayconstant.RelayModeImagesEdits, info.RelayMode)
				info.ChannelId = 102
				info.ChannelOtherSettings = dto.ChannelOtherSettings{}
				url, err = a.GetRequestURL(info)
				require.NoError(t, err)
				require.Equal(t, "https://example.test/v1/images/edits", url)
				_, err = a.ConvertImageRequest(c, info, dto.ImageRequest{Model: name})
				require.NoError(t, err, "native fallback must retain readable uploaded files")
			}
		}
	}
}
