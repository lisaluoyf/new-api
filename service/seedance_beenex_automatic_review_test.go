package service

import (
	"context"
	"errors"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type beeNexReviewTransport func(*http.Request) (*http.Response, error)

func (f beeNexReviewTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBeeNexRetainedAutomaticReview(t *testing.T) {
	for _, name := range []string{"seedance-2.0", "seedance-2.5"} {
		for _, tc := range []struct {
			name, first, review                                               string
			status, sends, actions                                            int
			secondUnknown, materialUnknown, missingCredentials, existingAsset bool
			transientRead, persistentRead                                     bool
			native                                                            bool
		}{
			{name: "active", review: "Active", sends: 2, actions: 3},
			{name: "native_active", native: true, review: "Active", sends: 2, actions: 3},
			{name: "transient_read", review: "Active", transientRead: true, sends: 2, actions: 4},
			{name: "persistent_read", review: "Active", persistentRead: true, sends: 1, actions: 5},
			{name: "rejected", review: "Failed", sends: 1, actions: 3},
			{name: "copyright", first: `{"error":{"code":"InputImageSensitiveContentDetected.PolicyViolation"}}`, sends: 1},
			{name: "generic_sensitive", first: `{"error":{"code":"InputImageSensitiveContentDetected"}}`, sends: 1},
			{name: "unauthorized", status: 401, sends: 1},
			{name: "server_error", status: 500, sends: 1},
			{name: "accepted_id", first: `{"id":"already-created","error":{"code":"InputImageSensitiveContentDetected.PrivacyInformation"}}`, sends: 1},
			{name: "nested_accepted_id", first: `{"data":{"task_id":"already-created"},"error":{"code":"InputImageSensitiveContentDetected.PrivacyInformation"}}`, sends: 1},
			{name: "second_unknown", review: "Active", secondUnknown: true, sends: 2, actions: 3},
			{name: "material_unknown", materialUnknown: true, sends: 1, actions: 1},
			{name: "missing_credentials", missingCredentials: true, sends: 1},
			{name: "existing_asset", existingAsset: true, sends: 1},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				db := seedanceTestDB(t)
				dir := t.TempDir()
				t.Setenv("BEENEX_ASSET_CREDENTIALS_DIR", dir)
				if !tc.missingCredentials {
					require.NoError(t, os.WriteFile(filepath.Join(dir, "channel-274.json"), []byte(`{"access_key_id":"beenex-ak","secret_access_key":"beenex-sk","region":"cn-beijing","project_name":"beenex-project"}`), 0600))
				}
				base := "https://beenexapi.phone580.com"
				require.NoError(t, db.Create(&model.Channel{Id: 274, Type: 1, Status: 1, Key: "video-private-key", BaseURL: &base, Models: name, Group: "default"}).Error)
				model.InitChannelCache()
				actions := 0
				reads := 0
				old := http.DefaultTransport
				http.DefaultTransport = beeNexReviewTransport(func(r *http.Request) (*http.Response, error) {
					actions++
					require.Equal(t, "beenexapi.phone580.com", r.URL.Host)
					require.Equal(t, "/post-proxy/", r.URL.Path)
					require.True(t, strings.HasPrefix(r.Header.Get("Authorization"), "HMAC-SHA256 Credential=beenex-ak/"))
					require.NotContains(t, r.Header.Get("Authorization"), "video-private-key")
					var fields map[string]any
					require.NoError(t, common.DecodeJson(r.Body, &fields))
					require.NotContains(t, fields, "ProjectName")
					require.NotContains(t, fields, "Moderation")
					if tc.materialUnknown {
						return nil, errors.New("unconfirmed")
					}
					var body string
					switch r.URL.Query().Get("Action") {
					case "CreateAssetGroup":
						require.Equal(t, "AIGC", fields["GroupType"])
						body = `{"Result":{"Id":"group-reviewed"}}`
					case "CreateAsset":
						require.Equal(t, "https://example.com/image.png", fields["URL"])
						require.Equal(t, "Image", fields["AssetType"])
						require.Equal(t, "group-reviewed", fields["GroupId"])
						body = `{"Result":{"Id":"asset-reviewed"}}`
					case "GetAsset":
						reads++
						if tc.persistentRead || tc.transientRead && reads == 1 {
							body = `{"Result":{"Error":{"Code":"InternalError","Message":"upstream request failed"}}}`
							break
						}
						require.Equal(t, "asset-reviewed", fields["Id"])
						body = `{"Result":{"Id":"asset-reviewed","ProjectName":"beenex-project","Status":"` + tc.review + `","Error":{"Message":"Image rejected beenex-ak beenex-sk video-private-key"}}}`
					default:
						t.Fatalf("unexpected action: %s", r.URL.RawQuery)
					}
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
				})
				t.Cleanup(func() { http.DefaultTransport = old })
				source := "https://example.com/image.png"
				if tc.existingAsset {
					source = "asset://already-reviewed"
				}
				raw := `{"model":"upstream-hy","seed":9223372036854775806,"prompt":"A synthetic character waves","metadata":{"content":[{"type":"image_url","role":"first_frame","image_url":{"url":"` + source + `","detail":"high"}},{"type":"audio_url","role":"reference_audio","audio_url":{"url":"https://example.com/audio.mp3"}}],"duration":4,"seed":0,"generate_audio":false,"watermark":false,"ratio":"9:16"}}`
				if tc.native {
					raw = `{"model":"upstream-hy","duration":-1,"seed":0,"generate_audio":false,"watermark":false,"ratio":"adaptive","content":[{"type":"text","text":"Edit video"},{"type":"image_url","role":"first_frame","image_url":{"url":"` + source + `","detail":"high"}},{"type":"audio_url","role":"reference_audio","audio_url":{"url":"https://example.com/audio.mp3"}}]}`
				}
				sends := 0
				c := seedanceContext(1)
				first := tc.first
				if first == "" {
					first = `{"error":{"code":"InputImageSensitiveContentDetected.PrivacyInformation"}}`
				}
				status := tc.status
				if status == 0 {
					status = 400
				}
				resp, err := DoXingchiAutomaticReview(c, 274, name, "video-private-key", "task_review", strings.NewReader(raw), func(r io.Reader) (*http.Response, error) {
					sends++
					out, e := io.ReadAll(r)
					require.NoError(t, e)
					if sends == 1 {
						require.Equal(t, raw, string(out))
						return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(first))}, nil
					}
					require.Contains(t, string(out), `"url":"asset://asset-reviewed"`)
					if !tc.native {
						require.Contains(t, string(out), `"seed":9223372036854775806`)
					} else {
						require.NotContains(t, string(out), `"metadata"`)
						require.Contains(t, string(out), `"duration":-1`)
					}
					require.Contains(t, string(out), `"seed":0`)
					require.Contains(t, string(out), `"generate_audio":false`)
					require.Contains(t, string(out), `"watermark":false`)
					require.Contains(t, string(out), `"role":"first_frame"`)
					require.Contains(t, string(out), `"url":"https://example.com/audio.mp3"`)
					require.Contains(t, string(out), `"detail":"high"`)
					require.NotContains(t, string(out), source)
					if tc.secondUnknown {
						return nil, errors.New("unknown second submission")
					}
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"created"}`))}, nil
				})
				if tc.secondUnknown {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
					out, e := io.ReadAll(resp.Body)
					require.NoError(t, e)
					resp.Body.Close()
					if tc.persistentRead {
						require.Equal(t, 502, resp.StatusCode)
						require.Contains(t, string(out), "material_query_unavailable")
						require.True(t, c.GetBool("xingchi_automatic_review_rejected"))
					}
					if tc.review == "Failed" {
						require.Contains(t, string(out), "Image rejected")
						for _, secret := range []string{"beenex-ak", "beenex-sk", "video-private-key"} {
							require.NotContains(t, string(out), secret)
						}
						require.True(t, c.GetBool("xingchi_automatic_review_rejected"))
					}
				}
				require.Equal(t, tc.sends, sends)
				require.Equal(t, tc.actions, actions)
				if tc.review == "Active" && !tc.persistentRead {
					var asset model.SeedanceResource
					require.NoError(t, db.Where("channel_id = ? AND kind = ?", 274, "asset").First(&asset).Error)
					require.Equal(t, "Active", asset.Status)
					require.NotEmpty(t, asset.CredentialFingerprint)
					require.Contains(t, asset.RequestData, `"automatic_review":true`)
					// Material context remains valid for subsequent manual queries and reuse.
					creds, e := loadBeeNexAssetCredentials(274)
					require.NoError(t, e)
					require.NoError(t, validateBeeNexAssetContext(&asset, creds))
				}
				if tc.materialUnknown {
					_, e := prepareRetainedSeedanceAutomaticReview(seedanceContext(1), 274, name, "video-private-key", "task_review", []byte(raw))
					require.Error(t, e)
					require.Equal(t, 1, actions)
				}
			})
		}
	}
}

func TestBeeNexAutomaticReviewCancelledBeforeMaterials(t *testing.T) {
	db := seedanceTestDB(t)
	base := "https://beenexapi.phone580.com"
	require.NoError(t, db.Create(&model.Channel{Id: 274, Type: 1, Status: 1, Key: "video-private-key", BaseURL: &base, Models: "seedance-2.5", Group: "default"}).Error)
	model.InitChannelCache()
	c := seedanceContext(1)
	ctx, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(ctx)
	// A cancelled request cannot create a group, asset or second video.
	calls := 0
	old := http.DefaultTransport
	http.DefaultTransport = beeNexReviewTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected") })
	t.Cleanup(func() { http.DefaultTransport = old })
	_, err := prepareRetainedSeedanceAutomaticReview(c, 274, "seedance-2.5", "video-private-key", "task_cancelled", []byte(`{"metadata":{"content":[{"type":"image_url","image_url":{"url":"https://example.com/image.png"}}]}}`))
	require.Error(t, err)
	require.Zero(t, calls)
}

func TestBeeNexWrappedPrivacyRejection(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		unwrap     bool
	}{
		{"observed", `{"code":"fail_to_fetch_task","data":null,"message":"{\"error\":{\"code\":\"InputImageSensitiveContentDetected.PrivacyInformation\"}}"}`, true},
		{"copyright", `{"code":"fail_to_fetch_task","data":null,"message":"{\"error\":{\"code\":\"InputImageSensitiveContentDetected.PolicyViolation\"}}"}`, false},
		{"generic", `{"code":"fail_to_fetch_task","data":null,"message":"input image may contain real person"}`, false},
		{"accepted_data", `{"code":"fail_to_fetch_task","data":{"id":"created"},"message":"{\"error\":{\"code\":\"InputImageSensitiveContentDetected.PrivacyInformation\"}}"}`, false},
		{"accepted_nested", `{"code":"fail_to_fetch_task","data":null,"message":"{\"id\":\"created\",\"error\":{\"code\":\"InputImageSensitiveContentDetected.PrivacyInformation\"}}"}`, false},
		{"different_wrapper", `{"code":"unknown","data":null,"message":"{\"error\":{\"code\":\"InputImageSensitiveContentDetected.PrivacyInformation\"}}"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := NormalizeBeeNexAutomaticReviewResponse(&http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(tc.body))})
			require.NoError(t, err)
			defer resp.Body.Close()
			raw, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			if tc.unwrap {
				require.JSONEq(t, `{"error":{"code":"InputImageSensitiveContentDetected.PrivacyInformation"}}`, string(raw))
			} else {
				require.Equal(t, tc.body, string(raw))
			}
		})
	}
}
