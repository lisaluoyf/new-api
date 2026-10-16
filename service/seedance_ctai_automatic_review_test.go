package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestCtaiRetainedAutomaticReview(t *testing.T) {
	for _, tc := range []struct {
		name, kind, review, first string
		secondUnknown             bool
		sends, actions            int
	}{
		{name: "image_active", kind: "image_url", review: "Active", first: automaticTokenHubRejection, sends: 2, actions: 2},
		{name: "video_active", kind: "video_url", review: "Active", first: `{"error":{"code":"InputVideoSensitiveContentDetected.PrivacyInformation"}}`, sends: 2, actions: 2},
		{name: "review_rejected", kind: "image_url", review: "Failed", first: automaticTokenHubRejection, sends: 1, actions: 2},
		{name: "copyright", kind: "image_url", first: `{"error":{"code":"InputImageSensitiveContentDetected.PolicyViolation"}}`, sends: 1},
		{name: "already_created", kind: "image_url", first: `{"id":"created","error":{"code":"InputImageSensitiveContentDetected.PrivacyInformation"}}`, sends: 1},
		{name: "unconfirmed_retry", kind: "image_url", review: "Active", first: automaticTokenHubRejection, secondUnknown: true, sends: 2, actions: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := seedanceTestDB(t)
			base := "https://ai.ctaigw.cn"
			require.NoError(t, db.Create(&model.Channel{Id: 298, Type: 54, Key: "ctai-private-key", BaseURL: &base, Status: 1, Models: "seedance-2.5", Group: "default"}).Error)
			model.InitChannelCache()
			calls := 0
			old := http.DefaultTransport
			http.DefaultTransport = automaticTokenHubTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, "ai.ctaigw.cn", r.URL.Host)
				require.Equal(t, "Bearer ctai-private-key", r.Header.Get("Authorization"))
				var body string
				if r.Method == http.MethodPost {
					require.Equal(t, "/v1/api/assets/upload", r.URL.Path)
					var fields map[string]any
					require.NoError(t, common.DecodeJson(r.Body, &fields))
					require.Equal(t, "https://example.com/reference.png", fields["url"])
					require.NotContains(t, fields, "GroupId")
					expected := "Image"
					if tc.kind == "video_url" {
						expected = "Video"
					}
					require.Equal(t, expected, fields["asset_type"])
					body = `{"code":0,"data":{"Id":"369481078005587968"}}`
				} else {
					require.Equal(t, "/v1/api/assets/369481078005587968", r.URL.Path)
					body = `{"code":0,"data":{"Id":"369481078005587968","Status":"` + tc.review + `","FailureReason":"Image did not pass review; ctai-private-key https://private.example/signed"}}`
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = old })
			raw := `{"model":"cdance2.5-0807","content":[{"type":"` + tc.kind + `","role":"reference_image","` + tc.kind + `":{"url":"https://example.com/reference.png","detail":"high"}}],"duration":4,"generate_audio":false,"watermark":false,"seed":9223372036854775806}`
			sends := 0
			c := seedanceContext(1)
			resp, err := DoXingchiAutomaticReview(c, 298, "seedance-2.5", "ctai-private-key", "task_ctai", strings.NewReader(raw), func(r io.Reader) (*http.Response, error) {
				sends++
				body, e := io.ReadAll(r)
				require.NoError(t, e)
				if sends == 1 {
					return &http.Response{StatusCode: 400, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.first))}, nil
				}
				require.Contains(t, string(body), `"url":"asset://369481078005587968"`)
				require.Contains(t, string(body), `"seed":9223372036854775806`)
				require.Contains(t, string(body), `"generate_audio":false`)
				require.Contains(t, string(body), `"watermark":false`)
				require.Contains(t, string(body), `"detail":"high"`)
				if tc.secondUnknown {
					return nil, errors.New("unconfirmed transport")
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader([]byte(`{"id":"created"}`)))}, nil
			})
			if tc.secondUnknown {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				out, e := io.ReadAll(resp.Body)
				require.NoError(t, e)
				resp.Body.Close()
				if tc.review == "Failed" {
					require.Contains(t, string(out), "Image did not pass review")
					require.NotContains(t, string(out), "ctai-private-key")
					require.NotContains(t, string(out), "private.example")
					require.True(t, c.GetBool("xingchi_automatic_review_rejected"))
				}
			}
			require.Equal(t, tc.sends, sends)
			require.Equal(t, tc.actions, calls)
			var resources []model.SeedanceResource
			require.NoError(t, db.Where("channel_id = ?", 298).Find(&resources).Error)
			if tc.actions > 0 {
				require.Len(t, resources, 2)
			} else {
				require.Empty(t, resources)
			}
		})
	}
}

func TestCtaiMaterialTransportFailuresAndScope(t *testing.T) {
	base := "https://ai.ctaigw.cn"
	ch := &model.Channel{Id: 298, Type: 54, BaseURL: &base, Key: "ctai-private-key"}
	require.True(t, IsCtaiSeedanceBaseURL(base))
	for _, bad := range []string{"http://ai.ctaigw.cn", "https://ai.ctaigw.cn.evil.example", "https://user@ai.ctaigw.cn", "https://ai.ctaigw.cn?x=1", "https://ai.ctaigw.cn/v1"} {
		require.False(t, IsCtaiSeedanceBaseURL(bad))
	}
	for _, tc := range []struct {
		name, body string
		status     int
		network    bool
	}{
		{name: "permission_denied", status: 403, body: `{"error":{"message":"not whitelisted ctai-private-key"}}`},
		{name: "server_failure", status: 500, body: `{"error":{"message":"internal"}}`},
		{name: "application_failure", status: 200, body: `{"code":7,"message":"failed"}`},
		{name: "missing_id", status: 200, body: `{"code":0,"data":{}}`},
		{name: "numeric_id_rejected", status: 200, body: `{"code":0,"data":{"Id":369481078005587968}}`},
		{name: "network_unknown", network: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			old := http.DefaultTransport
			http.DefaultTransport = automaticTokenHubTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if tc.network {
					return nil, errors.New("unknown")
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = old })
			_, err := ctaiAssetRequest(context.Background(), ch, "ctai-private-key", "CreateAsset", map[string]any{"URL": "https://example.com/image.png", "AssetType": "Image", "Name": "test"}, true)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "ctai-private-key")
			require.Equal(t, 1, calls)
		})
	}
	_, err := ctaiAssetRequest(context.Background(), ch, "ctai-private-key", "CreateVisualValidateSession", nil, false)
	require.Error(t, err)
	_, err = ctaiAssetRequest(context.Background(), ch, "ctai-private-key", "GetAsset", map[string]any{"Id": "../../other"}, false)
	require.Error(t, err)
}

func TestCtaiUnconfirmedUploadIsNotRepeated(t *testing.T) {
	db := seedanceTestDB(t)
	base := "https://ai.ctaigw.cn"
	require.NoError(t, db.Create(&model.Channel{Id: 298, Type: 54, Key: "ctai-private-key", BaseURL: &base, Status: 1, Models: "seedance-2.5", Group: "default"}).Error)
	model.InitChannelCache()
	calls := 0
	old := http.DefaultTransport
	http.DefaultTransport = automaticTokenHubTransport(func(r *http.Request) (*http.Response, error) { calls++; return nil, errors.New("unconfirmed upload") })
	t.Cleanup(func() { http.DefaultTransport = old })
	raw := []byte(`{"content":[{"type":"image_url","image_url":{"url":"https://example.com/image.png"}}]}`)
	for i := 0; i < 2; i++ {
		_, err := prepareRetainedSeedanceAutomaticReview(seedanceContext(1), 298, "seedance-2.5", "ctai-private-key", "task_same", raw)
		require.Error(t, err)
	}
	require.Equal(t, 1, calls)
	var resources []model.SeedanceResource
	require.NoError(t, db.Where("channel_id = ?", 298).Find(&resources).Error)
	require.Len(t, resources, 2)
}

func TestCtaiAdminPinnedMaterialScope(t *testing.T) {
	db := seedanceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}))
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "ctai-admin", AffCode: "ctai-admin", Role: 100, Status: 1}).Error)
	require.NoError(t, db.Create(&model.User{Id: 2, Username: "ctai-user", AffCode: "ctai-user", Role: 1, Status: 1}).Error)
	base := "https://ai.ctaigw.cn"
	ch := &model.Channel{Id: 298, Type: 54, Key: "private-key", BaseURL: &base, Status: 1, Models: "seedance-2.5", Group: "ctai_seedance_acceptance"}
	c := seedanceContext(1)
	require.False(t, ctaiMaterialAdminPinned(c, ch, "seedance-2.5"))
	common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, "298")
	require.True(t, ctaiMaterialAdminPinned(c, ch, "seedance-2.5"))
	require.False(t, ctaiMaterialAdminPinned(c, ch, "seedance-2.0"))
	ch.Status = 2
	require.False(t, ctaiMaterialAdminPinned(c, ch, "seedance-2.5"))
	ch.Status = 1
	c = seedanceContext(2)
	common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, "298")
	require.False(t, ctaiMaterialAdminPinned(c, ch, "seedance-2.5"))
}
