package middleware

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel/task/apimartvideo"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSeedanceReferencesReachOutboundRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, model := range []string{"seedance-2.5", "seedance-2.0"} {
		for _, path := range []string{"/v1/videos", "/v1/videos/generations", "/v1/video/generations"} {
			for _, mode := range []string{"json_urls", "json_input_reference", "role_image", "text"} {
				t.Run(model+path+"/"+mode, func(t *testing.T) {
					payload := map[string]any{"model": model, "prompt": "Move the reference scene", "seconds": "8", "size": "720x1280", "generate_audio": false, "watermark": false, "seed": 0}
					body := bytes.NewBuffer(nil)
					ct := "application/json"
					expectedCount := 1
					switch mode {
					case "json_urls":
						payload["image_urls"] = []string{"https://example.com/reference.png"}
					case "json_input_reference":
						payload["input_reference"] = "https://example.com/reference.png"
					case "role_image":
						payload["image_with_roles"] = []any{map[string]any{"url": "https://example.com/reference.png", "role": "reference_image"}}
					case "text":
						expectedCount = 0
					}
					raw, e := common.Marshal(payload)
					require.NoError(t, e)
					body.Write(raw)
					router := gin.New()
					router.Use(PrepareSeedanceAssetGeneration())
					var sent map[string]any
					router.POST(path, func(c *gin.Context) {
						defer common.CleanupBodyStorage(c)
						info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "mapped-seedance"}, TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
						a := &apimartvideo.TaskAdaptor{}
						e := a.ValidateRequestAndSetAction(c, info)
						require.Nil(t, e)
						if e != nil {
							return
						}
						req, err := relaycommon.GetTaskRequest(c)
						require.NoError(t, err)
						require.Equal(t, expectedCount, service.BuildVideoRequestDataForLog(&req)["actual_image_count"])
						if expectedCount > 0 {
							require.Equal(t, constant.TaskActionGenerate, info.Action)
						} else {
							require.Equal(t, constant.TaskActionTextGenerate, info.Action)
						}
						reader, err := a.BuildRequestBody(c, info)
						require.NoError(t, err)
						raw, err := io.ReadAll(reader)
						require.NoError(t, err)
						require.NoError(t, common.Unmarshal(raw, &sent))
						c.Status(200)
					})
					r := httptest.NewRequest(http.MethodPost, path, body)
					r.Header.Set("Content-Type", ct)
					out := httptest.NewRecorder()
					router.ServeHTTP(out, r)
					require.Equal(t, 200, out.Code, out.Body.String())
					require.Equal(t, "mapped-seedance", sent["model"])
					require.EqualValues(t, 8, sent["duration"])
					require.Equal(t, "9:16", sent["aspect_ratio"])
					require.Equal(t, false, sent["generate_audio"])
					require.Equal(t, false, sent["watermark"])
					require.EqualValues(t, 0, sent["seed"])
					if mode == "role_image" {
						require.Len(t, sent["image_with_roles"], 1)
					} else if expectedCount > 0 {
						urls := sent["image_urls"].([]any)
						require.Len(t, urls, 1)
						require.Equal(t, "https://example.com/reference.png", urls[0])
					}
				})
			}
		}
	}
}

func TestSeedanceMultipartRejectsInvalidReferenceBeforeSubmission(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		bytes       []byte
	}{{"empty", "input_reference", nil}, {"not_image", "input_reference", []byte("invalid image")}, {"unknown_field", "attachment", []byte("invalid image")}} {
		t.Run(tc.name, func(t *testing.T) {
			body := bytes.NewBuffer(nil)
			w := multipart.NewWriter(body)
			require.NoError(t, w.WriteField("model", "seedance-2.5"))
			require.NoError(t, w.WriteField("prompt", "scene"))
			p, e := w.CreateFormFile(tc.field, "reference.png")
			require.NoError(t, e)
			_, e = p.Write(tc.bytes)
			require.NoError(t, e)
			require.NoError(t, w.Close())
			router := gin.New()
			router.Use(PrepareSeedanceAssetGeneration())
			called := false
			router.POST("/v1/videos", func(c *gin.Context) { called = true; c.Status(200) })
			r := httptest.NewRequest(http.MethodPost, "/v1/videos", body)
			r.Header.Set("Content-Type", w.FormDataContentType())
			out := httptest.NewRecorder()
			router.ServeHTTP(out, r)
			require.Equal(t, 400, out.Code, out.Body.String())
			require.False(t, called)
		})
	}
}
