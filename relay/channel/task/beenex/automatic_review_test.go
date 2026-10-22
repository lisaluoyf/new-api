package beenex

import (
	"errors"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type beeNexAutoTransport func(*http.Request) (*http.Response, error)

func (f beeNexAutoTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBeeNexAutomaticReviewThroughAdaptor(t *testing.T) {
	for _, name := range []string{"seedance-2.0", "seedance-2.5"} {
		for _, outcome := range []string{"accepted", "review_rejected", "second_unknown", "second_rejected"} {
			t.Run(name+"/"+outcome, func(t *testing.T) {
				oldDB, oldMemory := model.DB, common.MemoryCacheEnabled
				db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
				require.NoError(t, err)
				require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.SeedanceResource{}))
				model.DB, common.MemoryCacheEnabled = db, true
				t.Cleanup(func() {
					model.DB, common.MemoryCacheEnabled = oldDB, oldMemory
					if oldDB != nil && oldMemory {
						model.InitChannelCache()
					}
				})
				base := "https://beenexapi.phone580.com"
				require.NoError(t, db.Create(&model.Channel{Id: 274, Type: 1, Status: 1, Key: "video-key", BaseURL: &base, Models: name, Group: "default"}).Error)
				model.InitChannelCache()
				dir := t.TempDir()
				t.Setenv("BEENEX_ASSET_CREDENTIALS_DIR", dir)
				require.NoError(t, os.WriteFile(filepath.Join(dir, "channel-274.json"), []byte(`{"access_key_id":"beenex-ak","secret_access_key":"beenex-sk","region":"cn-beijing","project_name":"beenex-project"}`), 0600))
				sends, actions := 0, 0
				transport := beeNexAutoTransport(func(r *http.Request) (*http.Response, error) {
					var fields map[string]any
					require.NoError(t, common.DecodeJson(r.Body, &fields))
					body := ""
					status := 200
					if r.URL.Path == "/v1/video/generations" {
						sends++
						require.Equal(t, "Bearer video-key", r.Header.Get("Authorization"))
						if sends == 1 {
							status = 400
							body = `{"error":{"code":"InputImageSensitiveContentDetected.PrivacyInformation"}}`
						} else {
							require.Equal(t, 2, sends)
							meta := fields["metadata"].(map[string]any)
							ref := meta["content"].([]any)[0].(map[string]any)
							require.Equal(t, "asset://asset-reviewed", ref["image_url"].(map[string]any)["url"])
							require.Equal(t, false, meta["generate_audio"])
							if outcome == "second_unknown" {
								return nil, errors.New("lost acceptance")
							}
							body = `{"id":"native-created"}`
							if outcome == "second_rejected" {
								status = 400
								body = `{"error":{"code":"InputImageSensitiveContentDetected.PrivacyInformation"}}`
							}
						}
					} else {
						actions++
						require.Equal(t, "/post-proxy/", r.URL.Path)
						require.True(t, strings.HasPrefix(r.Header.Get("Authorization"), "HMAC-SHA256 Credential=beenex-ak/"))
						switch r.URL.Query().Get("Action") {
						case "CreateAssetGroup":
							body = `{"Result":{"Id":"group-reviewed"}}`
						case "CreateAsset":
							body = `{"Result":{"Id":"asset-reviewed"}}`
						case "GetAsset":
							review := "Active"
							if outcome == "review_rejected" {
								review = "Failed"
							}
							body = `{"Result":{"Id":"asset-reviewed","ProjectName":"beenex-project","Status":"` + review + `"}}`
						default:
							t.Fatal(r.URL.String())
						}
					}
					return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
				})
				service.InitHttpClient()
				client := service.GetHttpClient()
				oldRelayTransport := client.Transport
				oldDefaultTransport := http.DefaultTransport
				client.Transport = transport
				http.DefaultTransport = transport
				t.Cleanup(func() { client.Transport = oldRelayTransport; http.DefaultTransport = oldDefaultTransport })
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", nil)
				c.Request.Header.Set("Content-Type", "application/json")
				c.Set("id", 1)
				common.SetContextKey(c, constant.ContextKeyTokenGroup, "default")
				info := &relaycommon.RelayInfo{UserId: 1, OriginModelName: name, TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task-auto"}, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 274, ChannelBaseUrl: base, ApiKey: "video-key", UpstreamModelName: "upstream-hy"}}
				a := &TaskAdaptor{}
				a.Init(info)
				raw := `{"model":"upstream-hy","prompt":"a synthetic character","metadata":{"content":[{"type":"image_url","image_url":{"url":"https://example.com/reference.png"},"role":"reference_image"}],"generate_audio":false,"duration":4}}`
				resp, err := a.DoRequest(c, info, strings.NewReader(raw))
				if outcome == "second_unknown" {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
					defer resp.Body.Close()
					expected := 200
					if outcome != "accepted" {
						expected = 400
					}
					require.Equal(t, expected, resp.StatusCode)
				}
				wantSends := 2
				if outcome == "review_rejected" {
					wantSends = 1
				}
				require.Equal(t, wantSends, sends)
				require.Equal(t, 3, actions)

			})
		}
	}
}
