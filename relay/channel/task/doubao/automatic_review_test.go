package doubao

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type reviewTransport func(*http.Request) (*http.Response, error)

func (f reviewTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAutomaticReviewRetriesGenerationOnlyOnceAfterActive(t *testing.T) {
	for _, tc := range []struct {
		name                                              string
		first                                             int
		review                                            string
		second, wantStatus, wantGenerations, wantControls int
	}{
		{"active", 400, "Active", 200, 200, 2, 3},
		{"review_failed", 400, "Failed", 200, 400, 1, 3},
		{"server_failure", 500, "Active", 200, 500, 1, 0},
		{"second_rejected", 400, "Active", 400, 400, 2, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			originalDB, originalMemory := model.DB, common.MemoryCacheEnabled
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			require.NoError(t, err)
			model.DB = db
			common.MemoryCacheEnabled = false
			t.Cleanup(func() { model.DB = originalDB; common.MemoryCacheEnabled = originalMemory })
			require.NoError(t, db.AutoMigrate(&model.SeedanceResource{}, &model.Channel{}))
			base := "https://ark.cn-beijing.volces.com"
			require.NoError(t, db.Create(&model.Channel{Id: 272, Type: constant.ChannelTypeDoubaoVideo, Key: "inference-key", BaseURL: &base, Status: 1, Models: "seedance-2.0-mini", Group: "default"}).Error)
			dir := t.TempDir()
			t.Setenv("BYTEPLUS_ASSET_CREDENTIALS_DIR", dir)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "channel-272.json"), []byte(`{"access_key_id":"test-ak","secret_access_key":"test-sk","region":"cn-beijing","project_name":"default"}`), 0600))
			generations, controls := 0, 0
			transport := reviewTransport(func(r *http.Request) (*http.Response, error) {
				var payload map[string]any
				require.NoError(t, common.DecodeJson(r.Body, &payload))
				status, body := 200, `{}`
				if r.URL.Host == "ark.cn-beijing.volces.com" {
					generations++
					require.Equal(t, "Bearer inference-key", r.Header.Get("Authorization"))
					if generations == 1 {
						status = tc.first
						body = `{"error":{"code":"InputImageSensitiveContentDetected.PrivacyInformation","message":"input contains a real person"}}`
					} else {
						require.Equal(t, 2, generations)
						content := payload["content"].([]any)
						img := content[1].(map[string]any)["image_url"].(map[string]any)
						require.Equal(t, "asset://native-image", img["url"])
						require.Equal(t, false, payload["generate_audio"])
						require.Equal(t, float64(0), payload["seed"])
						status = tc.second
						body = `{"id":"cgt-test"}`
					}
				} else {
					controls++
					require.Equal(t, "ark.cn-beijing.volcengineapi.com", r.URL.Host)
					require.NotContains(t, payload, "Moderation")
					switch r.URL.Query().Get("Action") {
					case "CreateAssetGroup":
						require.Equal(t, "AIGC", payload["GroupType"])
						body = `{"Result":{"Id":"native-group"}}`
					case "CreateAsset":
						body = `{"Result":{"Id":"native-image"}}`
					case "GetAsset":
						body = `{"Result":{"Status":"` + tc.review + `","Error":{"Code":"ReviewRejected","Message":"Reference image rejected by review"}}}`
					default:
						t.Fatalf("unexpected control action %s", r.URL.Query().Get("Action"))
					}
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			oldTransport := http.DefaultTransport
			http.DefaultTransport = transport
			t.Cleanup(func() { http.DefaultTransport = oldTransport })
			if service.GetHttpClient() == nil {
				service.InitHttpClient()
			}
			client := service.GetHttpClient()
			oldClientTransport := client.Transport
			client.Transport = transport
			t.Cleanup(func() { client.Transport = oldClientTransport })
			raw := []byte(`{"model":"doubao-seedance-2-0-mini-260615","content":[{"type":"text","text":"A person blinks"},{"type":"image_url","role":"reference_image","image_url":{"url":"https://example.com/photo.jpg"}}],"duration":4,"resolution":"480p","ratio":"adaptive","generate_audio":false,"seed":0}`)
			c, _ := seedanceContext(string(raw))
			c.Set("id", 1)
			common.SetContextKey(c, constant.ContextKeyTokenGroup, "default")
			info := &relaycommon.RelayInfo{OriginModelName: "seedance-2.0-mini", TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_test"}, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 272, ApiKey: "inference-key", ChannelBaseUrl: base}}
			adaptor := &TaskAdaptor{}
			adaptor.Init(info)
			resp, err := adaptor.DoRequest(c, info, bytes.NewReader(raw))
			require.NoError(t, err)
			defer resp.Body.Close()
			require.Equal(t, tc.wantStatus, resp.StatusCode)
			require.Equal(t, tc.wantGenerations, generations)
			require.Equal(t, tc.wantControls, controls)
			if tc.review == "Failed" {
				b, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.Contains(t, string(b), "Reference image rejected by review")
			}
		})
	}
}
