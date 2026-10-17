package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

type automaticTokenHubTransport func(*http.Request) (*http.Response, error)

func (f automaticTokenHubTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const automaticTokenHubBody = `{"model":"doubao-seedance-2-5-260628","content":[{"type":"text","text":"A synthetic character waves"},{"type":"image_url","role":"reference_image","image_url":{"url":"https://example.com/cartoon.png","detail":"high"}},{"type":"video_url","video_url":{"url":"https://example.com/video.mp4"},"role":"reference_video"}],"generate_audio":false,"watermark":false,"seed":9223372036854775806,"duration":4,"ratio":"adaptive"}`
const automaticTokenHubRejection = `{"error":{"code":"InputImageSensitiveContentDetected.PrivacyInformation","message":"input image may contain real person"}}`

func TestTokenHubAutomaticReviewSubmission(t *testing.T) {
	for _, tc := range []struct {
		name                               string
		status                             int
		firstBody, review                  string
		secondStatus                       int
		secondNetwork                      bool
		wantSends, wantActions, wantStatus int
		wantMessage                        string
		moderation                         bool
		createRejected                     bool
	}{
		{name: "active_default_skip", status: 400, firstBody: automaticTokenHubRejection, review: "Active", secondStatus: 200, wantSends: 2, wantActions: 3, wantStatus: 200},
		{name: "active_skip", moderation: true, status: 400, firstBody: automaticTokenHubRejection, review: "Active", secondStatus: 200, wantSends: 2, wantActions: 3, wantStatus: 200},
		{name: "skip_review_rejected", moderation: true, status: 400, firstBody: automaticTokenHubRejection, review: "Failed", wantSends: 1, wantActions: 3, wantStatus: 400, wantMessage: "Image did not pass review"},
		{name: "skip_create_rejected", moderation: true, createRejected: true, status: 400, firstBody: automaticTokenHubRejection, wantSends: 1, wantActions: 2, wantStatus: 400, wantMessage: "Library permission denied"},
		{name: "default_skip_create_rejected", createRejected: true, status: 400, firstBody: automaticTokenHubRejection, wantSends: 1, wantActions: 2, wantStatus: 400, wantMessage: "Library permission denied"},
		{name: "rejected_review", status: 400, firstBody: automaticTokenHubRejection, review: "Failed", wantSends: 1, wantActions: 3, wantStatus: 400, wantMessage: "Image did not pass review"},
		{name: "second_rejected", status: 400, firstBody: automaticTokenHubRejection, review: "Active", secondStatus: 400, wantSends: 2, wantActions: 3, wantStatus: 400},
		{name: "second_network_unknown", status: 400, firstBody: automaticTokenHubRejection, review: "Active", secondNetwork: true, wantSends: 2, wantActions: 3},
		{name: "copyright", status: 400, firstBody: `{"error":{"code":"InputImageSensitiveContentDetected.PolicyViolation"}}`, wantSends: 1, wantStatus: 400},
		{name: "generic_sensitive", status: 400, firstBody: `{"error":{"code":"InputImageSensitiveContentDetected"}}`, wantSends: 1, wantStatus: 400},
		{name: "server_error", status: 500, firstBody: automaticTokenHubRejection, wantSends: 1, wantStatus: 500},
		{name: "created_id_on_error", status: 400, firstBody: `{"id":"already-created","error":{"code":"InputImageSensitiveContentDetected.PrivacyInformation"}}`, wantSends: 1, wantStatus: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tokenHubTestCredentials(t)
			db := seedanceTestDB(t)
			base := "https://ai-tokenhub.ai"
			require.NoError(t, db.Create(&model.Channel{Id: 297, Type: 54, Key: "private-key", BaseURL: &base, Status: 1, Models: "seedance-2.5", Group: "default"}).Error)
			model.InitChannelCache()
			actions := []string{}
			old := http.DefaultTransport
			http.DefaultTransport = automaticTokenHubTransport(func(r *http.Request) (*http.Response, error) {
				require.Equal(t, "ai-tokenhub.ai", r.URL.Host)
				require.Equal(t, "/ark", r.URL.Path)
				require.Equal(t, "Bearer pak_test:psk_test", r.Header.Get("Authorization"))
				var fields map[string]any
				require.NoError(t, common.DecodeJson(r.Body, &fields))
				require.Equal(t, "default", fields["ProjectName"])
				action := r.URL.Query().Get("Action")
				if action == "CreateAsset" {
					require.Equal(t, map[string]any{"Strategy": "Skip"}, fields["Moderation"])
				} else {
					require.NotContains(t, fields, "Moderation")
				}
				actions = append(actions, action)
				var body string
				switch action {
				case "CreateAssetGroup":
					require.Equal(t, "AIGC", fields["GroupType"])
					body = `{"Result":{"Id":"native-group"}}`
				case "CreateAsset":
					require.Equal(t, "native-group", fields["GroupId"])
					require.Equal(t, "https://example.com/cartoon.png", fields["URL"])
					body = `{"Result":{"Id":"native-asset"}}`
				case "GetAsset":
					body = `{"Result":{"Status":"` + tc.review + `","Error":{"Code":"ReviewRejected","Message":"Image did not pass review; private-key https://private.example/signed"}}}`
				default:
					t.Fatalf("unexpected action (including deletion) %s", action)
				}
				status := http.StatusOK
				if action == "CreateAsset" && tc.createRejected {
					status = http.StatusForbidden
					body = `{"ResponseMetadata":{"Error":{"Code":"AccessDenied","Message":"Library permission denied; private-key pak_test psk_test https://private.example/signed"}}}`
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = old })
			c := seedanceContext(1)
			sends := 0
			requestBody := automaticTokenHubBody
			if tc.moderation {
				requestBody = strings.TrimSuffix(requestBody, "}") + `,"Moderation":{"Strategy":"Skip"}}`
			}
			resp, err := DoXingchiAutomaticReview(c, 297, "seedance-2.5", "private-key", "task_auto", strings.NewReader(requestBody), func(r io.Reader) (*http.Response, error) {
				sends++
				raw, e := io.ReadAll(r)
				require.NoError(t, e)
				status, body := tc.status, tc.firstBody
				if sends == 2 {
					require.Contains(t, string(raw), `"seed":9223372036854775806`)
					require.Contains(t, string(raw), `"generate_audio":false`)
					require.Contains(t, string(raw), `"watermark":false`)
					require.Contains(t, string(raw), `"detail":"high"`)
					var out, original map[string]any
					require.NoError(t, common.Unmarshal(raw, &out))
					require.NoError(t, common.Unmarshal([]byte(requestBody), &original))
					image := out["content"].([]any)[1].(map[string]any)["image_url"].(map[string]any)
					require.Equal(t, "asset://native-asset", image["url"])
					image["url"] = "https://example.com/cartoon.png"
					require.Equal(t, original, out)
					if tc.secondNetwork {
						return nil, errors.New("unconfirmed transport failure")
					}
					status, body = tc.secondStatus, `{"id":"created-video"}`
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader([]byte(body)))}, nil
			})
			if tc.secondNetwork {
				require.Error(t, err)
				require.Nil(t, resp)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.wantStatus, resp.StatusCode)
				response, e := io.ReadAll(resp.Body)
				require.NoError(t, e)
				require.NoError(t, resp.Body.Close())
				if tc.wantMessage != "" {
					require.Contains(t, string(response), tc.wantMessage)
					require.NotContains(t, string(response), "private-key")
					require.NotContains(t, string(response), "private.example")
					require.NotContains(t, string(response), "pak_test")
					require.NotContains(t, string(response), "psk_test")
					require.True(t, c.GetBool("xingchi_automatic_review_rejected"))
				}
			}
			require.Equal(t, tc.wantSends, sends)
			require.Len(t, actions, tc.wantActions)
			var resources []model.SeedanceResource
			require.NoError(t, db.Where("channel_id = ?", 297).Find(&resources).Error)
			if tc.wantActions > 0 {
				require.Len(t, resources, 2)
				for _, resource := range resources {
					var saved map[string]any
					require.NoError(t, common.UnmarshalJsonStr(resource.RequestData, &saved))
					require.Equal(t, true, saved["automatic_review"])
					require.Equal(t, "task_auto", saved["video_task_id"])
					if resource.Kind == "asset" {
						require.Equal(t, map[string]any{"Strategy": "Skip"}, saved["moderation"])
						require.Equal(t, !tc.createRejected, saved["moderation_forwarded"])
						dto := seedanceModerationDTO(&resource)
						require.Equal(t, "Skip", dto["requested"].(*SeedanceModeration).Strategy)
						require.Equal(t, !tc.createRejected, dto["forwarded"])
						require.Equal(t, false, dto["provider_effect_confirmed"])
					} else {
						require.Equal(t, map[string]any{"automatic_review": true, "video_task_id": "task_auto"}, saved)
						require.Nil(t, seedanceModerationDTO(&resource))
					}
				}
				if tc.name == "active_default_skip" {
					var asset model.SeedanceResource
					require.NoError(t, db.Where("channel_id = ? AND kind = ?", 297, "asset").First(&asset).Error)
					require.Empty(t, asset.ParentID)
					resolved, err := ResolveSeedanceAssetReferences(c, map[string]any{"model": "seedance-2.5", "image_urls": []any{"asset://" + asset.ID}}, 297, "private-key")
					require.NoError(t, err)
					require.Equal(t, []any{"asset://native-asset"}, resolved["image_urls"])
				}
			} else {
				require.Empty(t, resources)
			}
		})
	}
}

func TestTokenHubAutomaticReviewUnconfirmedCreateIsNotRepeated(t *testing.T) {
	tokenHubTestCredentials(t)
	db := seedanceTestDB(t)
	base := "https://ai-tokenhub.ai"
	require.NoError(t, db.Create(&model.Channel{Id: 297, Type: 54, Key: "private-key", BaseURL: &base, Status: 1}).Error)
	model.InitChannelCache()
	creates := 0
	old := http.DefaultTransport
	http.DefaultTransport = automaticTokenHubTransport(func(r *http.Request) (*http.Response, error) {
		creates++
		return nil, errors.New("connection lost after sending")
	})
	t.Cleanup(func() { http.DefaultTransport = old })
	c := seedanceContext(1)
	for i := 0; i < 2; i++ {
		_, err := prepareRetainedSeedanceAutomaticReview(c, 297, "seedance-2.5", "private-key", "task_uncertain", []byte(automaticTokenHubBody))
		require.Error(t, err)
	}
	require.Equal(t, 1, creates)
	var count int64
	require.NoError(t, db.Model(&model.SeedanceResource{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestTokenHubAutomaticReviewProviderFailureDetailsAndCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "provider_detail", true: "cancelled"}[cancelled], func(t *testing.T) {
			tokenHubTestCredentials(t)
			db := seedanceTestDB(t)
			base := "https://ai-tokenhub.ai"
			require.NoError(t, db.Create(&model.Channel{Id: 297, Type: 54, Key: "private-key", BaseURL: &base, Status: 1}).Error)
			model.InitChannelCache()
			c := seedanceContext(1)
			ctx, cancel := context.WithCancel(c.Request.Context())
			defer cancel()
			c.Request = c.Request.WithContext(ctx)
			old := http.DefaultTransport
			calls := 0
			http.DefaultTransport = automaticTokenHubTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				status, body := 400, `{"ResponseMetadata":{"Error":{"Code":"InvalidParameter","Message":"Image width must be at least 300px; private-key"}}}`
				if cancelled {
					cancel()
					status, body = 200, `{"Result":{"Id":"native-group"}}`
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = old })
			_, err := prepareRetainedSeedanceAutomaticReview(c, 297, "seedance-2.5", "private-key", "task_reject", []byte(automaticTokenHubBody))
			require.Error(t, err)
			if !cancelled {
				require.Contains(t, err.Error(), "Image width must be at least 300px")
				require.NotContains(t, err.Error(), "private-key")
			}
			require.Equal(t, 1, calls)
		})
	}
}

func tokenHubTestCredentials(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TOKENHUB_ASSET_CREDENTIALS_DIR", dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "channel-297.json"), []byte(`{"access_key_id":"pak_test","secret_access_key":"psk_test"}`), 0600))
}
func TestTokenHubResponseErrorAndCredentialBinding(t *testing.T) {
	tokenHubTestCredentials(t)
	db := seedanceTestDB(t)
	base := "https://ai-tokenhub.ai"
	ch := &model.Channel{Id: 297, Type: 54, Key: "video-private", BaseURL: &base, Status: 1}
	require.NoError(t, db.Create(ch).Error)
	r := &model.SeedanceResource{ChannelID: 297, SourceURL: "https://private.example/image"}
	require.NoError(t, bindTokenHubAssetContext(r))
	old := http.DefaultTransport
	calls := 0
	http.DefaultTransport = automaticTokenHubTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, "Bearer pak_test:psk_test", req.Header.Get("Authorization"))
		return &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(`{"ResponseError":{"Code":"InvalidParameter","Message":"Review refused pak_test psk_test https://private.example/image"}}`))}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = old })
	_, err := tokenHubAssetRequest(context.Background(), r, ch, "CreateAsset", nil, true)
	require.ErrorContains(t, err, "Review refused")
	require.NotContains(t, err.Error(), "pak_test")
	require.NotContains(t, err.Error(), "psk_test")
	require.NotContains(t, err.Error(), "https://")
	r.CredentialFingerprint = "changed"
	_, err = tokenHubAssetRequest(context.Background(), r, ch, "CreateAsset", nil, true)
	require.ErrorContains(t, err, "original account")
	require.Equal(t, 1, calls)
	require.False(t, IsTokenHubSeedanceBaseURL("https://ai-tokenhub.ai.evil.example"))
	require.False(t, IsTokenHubSeedanceBaseURL("http://ai-tokenhub.ai"))
	require.NoError(t, os.Chmod(filepath.Join(os.Getenv("TOKENHUB_ASSET_CREDENTIALS_DIR"), "channel-297.json"), 0644))
	_, err = loadTokenHubAssetCredentials(297)
	require.Error(t, err)
}

func TestTokenHubQueryResultSummary(t *testing.T) {
	raw := `{"id":"tenant-task","status":"succeeded","resultSummary":{"id":"private-provider-id","content":{"video_url":"https://example.com/video.mp4","last_frame_url":"https://example.com/frame.png"},"duration":4,"resolution":"480p","usage":{"completion_tokens":38400,"total_tokens":38400}}}`
	resp, err := NormalizeTokenHubVideoResponse(&http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(raw))})
	require.NoError(t, err)
	var d map[string]any
	require.NoError(t, common.DecodeJson(resp.Body, &d))
	require.Equal(t, "tenant-task", d["id"])
	require.Equal(t, "succeeded", d["status"])
	require.Equal(t, float64(4), d["duration"])
	require.Equal(t, "https://example.com/video.mp4", d["content"].(map[string]any)["video_url"])
	require.Equal(t, float64(38400), d["usage"].(map[string]any)["completion_tokens"])
	for _, status := range []string{"expired", "cancelled"} {
		resp, err = NormalizeTokenHubVideoResponse(&http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"tenant-task","status":"` + status + `"}`))})
		require.NoError(t, err)
		require.NoError(t, common.DecodeJson(resp.Body, &d))
		require.Equal(t, "failed", d["status"])
		require.Contains(t, d["error"].(map[string]any)["message"], status)
	}
}
