package openai

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSubrouterGeminiInlineAndMultipartReturnRequestedBase64(t *testing.T) {
	service.InitHttpClient()
	client := service.GetHttpClient()
	oldTransport := client.Transport
	t.Cleanup(func() { client.Transport = oldTransport })
	var pngBody bytes.Buffer
	require.NoError(t, png.Encode(&pngBody, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBody.Bytes())
	}))
	defer fixture.Close()
	fetch := system_setting.GetFetchSetting()
	oldFetch := *fetch
	fetch.AllowPrivateIp = true
	fetch.AllowedPorts = []string{strings.TrimPrefix(fixture.URL, "http://127.0.0.1:")}
	t.Cleanup(func() { *fetch = oldFetch })
	for _, kind := range []string{"generation", "inline", "multipart"} {
		t.Run(kind, func(t *testing.T) {
			mode, path := relayconstant.RelayModeImagesEdits, "/v1/images/edits"
			if kind == "generation" {
				mode, path = relayconstant.RelayModeImagesGenerations, "/v1/images/generations"
			}
			var body bytes.Buffer
			ct := "application/json"
			if kind == "multipart" {
				writer := multipart.NewWriter(&body)
				for k, v := range map[string]string{"model": "gemini-3-pro-image", "prompt": "edit", "resolution": "2K", "size": "1:1", "response_format": "b64_json"} {
					require.NoError(t, writer.WriteField(k, v))
				}
				part, err := writer.CreateFormFile("image", "ref.png")
				require.NoError(t, err)
				_, err = part.Write(pngBody.Bytes())
				require.NoError(t, err)
				require.NoError(t, writer.Close())
				ct = writer.FormDataContentType()
			} else {
				request := dto.ImageRequest{Model: "gemini-3-pro-image", Prompt: "edit", Size: "1:1", Resolution: "2K", ResponseFormat: "b64_json"}
				if kind == "inline" {
					request.ImageUrls = []string{"data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBody.Bytes())}
				}
				raw, err := common.Marshal(request)
				require.NoError(t, err)
				body.Write(raw)
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest("POST", path, bytes.NewReader(body.Bytes()))
			c.Request.Header.Set("Content-Type", ct)
			t.Cleanup(func() { common.CleanupBodyStorage(c) })
			request, err := helper.GetAndValidOpenAIImageRequest(c, mode)
			require.NoError(t, err)
			info := &relaycommon.RelayInfo{OriginModelName: "gemini-3-pro-image", RelayMode: mode, RequestURLPath: path, ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, ChannelBaseUrl: "https://subrouter.ai", UpstreamModelName: "gemini-3-pro-image-preview"}}
			mapped := *request
			mapped.Model = info.UpstreamModelName
			converted, err := (&Adaptor{}).ConvertImageRequest(c, info, mapped)
			require.NoError(t, err)
			var wire io.Reader
			if buffer, ok := converted.(*bytes.Buffer); ok {
				wire = buffer
			} else {
				raw, err := common.Marshal(converted)
				require.NoError(t, err)
				wire = bytes.NewReader(raw)
			}
			client.Transport = apimartGeminiTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "subrouter.ai" {
					return http.DefaultTransport.RoundTrip(r)
				}
				require.Equal(t, path, r.URL.Path)
				if kind == "generation" {
					var sent dto.ImageRequest
					require.NoError(t, common.DecodeJson(r.Body, &sent))
					require.Equal(t, "url", sent.ResponseFormat)
				} else {
					require.NoError(t, r.ParseMultipartForm(1<<20))
					defer r.MultipartForm.RemoveAll()
					require.Equal(t, "url", r.FormValue("response_format"))
					require.Equal(t, "2048x2048", r.FormValue("size"))
					require.Equal(t, "gemini-3-pro-image-preview", r.FormValue("model"))
					file, _, err := r.FormFile("image")
					require.NoError(t, err)
					actual, err := io.ReadAll(file)
					file.Close()
					require.NoError(t, err)
					require.Equal(t, pngBody.Bytes(), actual)
				}
				raw, err := common.Marshal(map[string]any{"created": 1, "data": []any{map[string]string{"url": fixture.URL + "/image.png"}}})
				require.NoError(t, err)
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(raw)), Request: r}, nil
			})
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, "https://subrouter.ai")
			response, err := (&Adaptor{}).DoRequest(c, info, wire)
			require.NoError(t, err)
			_, apiErr := OpenaiHandlerWithUsage(c, info, response.(*http.Response))
			require.Nil(t, apiErr)
			var result struct {
				Data []struct {
					Base64 string `json:"b64_json"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &result))
			require.Len(t, result.Data, 1)
			require.Equal(t, base64.StdEncoding.EncodeToString(pngBody.Bytes()), result.Data[0].Base64)
			require.Equal(t, "b64_json", request.ResponseFormat)
			require.Equal(t, "gemini-3-pro-image", request.Model)
			require.Equal(t, ct, c.GetHeader("Content-Type"))
		})
	}
}

func TestSubrouterGeminiRejectsMalformedInlineReferencesWithoutDownload(t *testing.T) {
	old := subrouterGeminiDownload
	subrouterGeminiDownload = func(string, ...string) (*http.Response, error) {
		t.Fatal("inline input must not enter downloader")
		return nil, nil
	}
	t.Cleanup(func() { subrouterGeminiDownload = old })
	for _, reference := range []string{"data:image/png;base64,%%%", "data:image/png,raw", "data:text/plain;base64,aGVsbG8=", "data:image/png;base64,", "data:image/png;base64," + strings.Repeat("A", base64.StdEncoding.EncodedLen(20<<20)+4)} {
		_, err := readSubrouterGeminiReference(reference, 1)
		require.Error(t, err)
		require.NotContains(t, err.Error(), reference)
	}
}
