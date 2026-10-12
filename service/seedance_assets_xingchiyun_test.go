package service

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type xingchiyunTestTransport func(*http.Request) (*http.Response, error)

func (f xingchiyunTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func xingchiyunTestLibrary(t *testing.T, handle func(string, map[string]any) (int, string)) *gorm.DB {
	t.Helper()
	db := seedanceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.ChannelModelPricing{}))
	base := "https://api.xingchiyun.com/v1/seedance"
	setting := `{"manual_group_ratio":1}`
	require.NoError(t, db.Create(&model.Channel{Id: 292, Type: constant.ChannelTypeDoubaoVideo, Key: "gateway-test-key", BaseURL: &base, Setting: &setting, Status: 1, Models: "seedance-2.5", Group: "default"}).Error)
	require.NoError(t, db.Create(&model.Ability{ChannelId: 292, Model: "seedance-2.5", Group: "default", Enabled: true}).Error)
	model.InitChannelCache()
	original := http.DefaultTransport
	http.DefaultTransport = xingchiyunTestTransport(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "https", r.URL.Scheme)
		require.Equal(t, "api.xingchiyun.com", r.URL.Host)
		require.Equal(t, "/v1/seedance", r.URL.Path)
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "2024-01-01", r.URL.Query().Get("Version"))
		require.Equal(t, "Bearer gateway-test-key", r.Header.Get("Authorization"))
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var fields map[string]any
		require.NoError(t, common.Unmarshal(raw, &fields))
		require.Equal(t, "default", fields["ProjectName"])
		require.NotContains(t, fields, "CallbackURL")
		require.NotContains(t, fields, "model")
		status, body := handle(r.URL.Query().Get("Action"), fields)
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	return db
}

func TestXingchiyunLibraryCRUDAndPinnedReferences(t *testing.T) {
	ready := false
	xingchiyunTestLibrary(t, func(action string, fields map[string]any) (int, string) {
		switch action {
		case "CreateAssetGroup":
			require.Equal(t, "AIGC", fields["GroupType"])
			return 200, `{"Result":{"Id":"group-20260917000042-a1b2c3"}}`
		case "CreateAsset":
			require.Equal(t, "group-20260917000042-a1b2c3", fields["GroupId"])
			require.Equal(t, "https://example.com/virtual.jpg", fields["URL"])
			return 200, `{"Result":{"Id":"asset-private"}}`
		case "GetAsset":
			require.Equal(t, "asset-private", fields["Id"])
			status := "Processing"
			if ready {
				status = "Active"
			}
			return 200, fmt.Sprintf(`{"Result":{"Id":"asset-private","Status":%q,"URL":"https://private.example/signed?X-Tos-Signature=secret"}}`, status)
		case "UpdateAsset", "UpdateAssetGroup":
			require.Equal(t, "renamed", fields["Name"])
			return 200, `{"Result":{}}`
		case "DeleteAsset", "DeleteAssetGroup":
			return 200, `{"Result":{}}`
		default:
			t.Fatalf("unexpected action %s", action)
			return 500, `{}`
		}
	})
	c := seedanceContext(1)
	capabilities, err := SeedancePortraitCapabilities(c, "seedance-2.5")
	require.NoError(t, err)
	require.Len(t, capabilities, 1)
	require.Equal(t, "official_h5", capabilities[0]["method"])
	require.Equal(t, false, capabilities[0]["hosted_callback"])
	task, err := SubmitSeedanceAssets(c, SeedanceAssetSubmission{Model: "seedance-2.5", Group: &SeedanceGroupInput{ChannelID: 292}, Assets: []SeedanceAssetInput{{URL: "https://example.com/virtual.jpg"}}})
	require.NoError(t, err)
	require.Equal(t, 292, task.ChannelID)
	require.NoError(t, PollSeedanceAssetTask(c, task))
	require.Equal(t, "processing", task.Status)
	ready = true
	require.NoError(t, PollSeedanceAssetTask(c, task))
	require.Equal(t, "completed", task.Status)
	var ids []string
	require.NoError(t, common.UnmarshalJsonStr(task.RequestData, &ids))
	asset, err := model.GetSeedanceResource(1, "asset", ids[0])
	require.NoError(t, err)
	require.Equal(t, "Active", asset.Status)
	require.Contains(t, SeedanceAssetDTO(asset)["url"], seedanceLibraryPath+"/assets/"+asset.ID+"/preview")
	require.Equal(t, "https://private.example/signed?X-Tos-Signature=secret", SeedanceAssetPreviewSource(asset))
	fields := map[string]any{"model": "seedance-2.5", "image_urls": []any{"asset://" + asset.ID}}
	pin, err := SeedanceAssetRouting(c, fields)
	require.NoError(t, err)
	require.Equal(t, 292, pin.ChannelID)
	resolved, err := ResolveSeedanceAssetReferences(c, fields, 292, "gateway-test-key")
	require.NoError(t, err)
	require.Equal(t, []any{"asset://asset-private"}, resolved["image_urls"])
	_, err = SeedanceAssetRouting(seedanceContext(2), fields)
	require.Error(t, err)
	_, err = ResolveSeedanceAssetReferences(c, fields, 273, "gateway-test-key")
	require.Error(t, err)
	group, err := model.GetSeedanceResource(1, "group", task.GroupID)
	require.NoError(t, err)
	name := "renamed"
	require.NoError(t, UpdateSeedanceResource(c, asset, &name, nil))
	require.NoError(t, UpdateSeedanceResource(c, group, &name, nil))
	require.Error(t, DeleteSeedanceResource(c, group))
	require.NoError(t, DeleteSeedanceResource(c, asset))
	require.NoError(t, DeleteSeedanceResource(c, group))
}

func TestXingchiyunVerificationPendingSuccessFailureAndExpiry(t *testing.T) {
	t.Setenv("SEEDANCE_CALLBACK_ORIGIN", "")
	phase, creates := "pending", 0
	db := xingchiyunTestLibrary(t, func(action string, fields map[string]any) (int, string) {
		if action == "CreateVisualValidateSession" {
			creates++
			return 200, `{"Result":{"Id":"group-placeholder","BytedToken":"opaque-token","H5Link":"https://ark.volcengine.com/verify?s=secret"}}`
		}
		require.Equal(t, "GetVisualValidateResult", action)
		require.Equal(t, "opaque-token", fields["BytedToken"])
		switch phase {
		case "pending":
			return 404, `{"ResponseMetadata":{"Error":{"Code":"NotFound.opaque-token","Message":"opaque-token not found"}}}`
		case "other404":
			return 404, `{"error":{"message":"opaque-token missing"}}`
		case "failure":
			return 400, `{"error":{"message":"validation is not pending or successful"}}`
		case "mismatch":
			return 200, `{"Result":{"GroupId":"wrong-group"}}`
		default:
			return 200, `{"Result":{"GroupId":"group-placeholder"}}`
		}
	})
	c := seedanceContext(1)
	c.Request.Header.Set("Idempotency-Key", "gateway-session-1")
	v, err := CreateSeedanceVerification(c, SeedanceVerificationInput{Model: "seedance-2.5", ChannelID: 292})
	require.NoError(t, err)
	require.Equal(t, "pending", v.Status)
	again, err := CreateSeedanceVerification(c, SeedanceVerificationInput{Model: "seedance-2.5", ChannelID: 292})
	require.NoError(t, err)
	require.Equal(t, v.ID, again.ID)
	require.Equal(t, 1, creates)
	require.NoError(t, RefreshSeedanceVerification(c, v))
	require.Equal(t, "pending", v.Status)
	var count int64
	require.NoError(t, db.Model(&model.SeedanceResource{}).Where("kind = ?", "group").Count(&count).Error)
	require.Zero(t, count)
	phase = "other404"
	err = RefreshSeedanceVerification(c, v)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "opaque-token")
	phase = "mismatch"
	require.Error(t, RefreshSeedanceVerification(c, v))
	phase = "success"
	require.NoError(t, RefreshSeedanceVerification(c, v))
	require.Equal(t, "completed", v.Status)
	require.Empty(t, v.UpstreamID)
	group, err := model.GetSeedanceResource(1, "group", v.GroupID)
	require.NoError(t, err)
	require.Equal(t, "real_person", group.GroupType)
	require.Equal(t, "group-placeholder", group.UpstreamID)
	encoded, err := common.Marshal(SeedanceVerificationDTO(v))
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "opaque-token")
	require.NotContains(t, string(encoded), "group-placeholder")
	c.Request.Header.Set("Idempotency-Key", "gateway-session-2")
	v, err = CreateSeedanceVerification(c, SeedanceVerificationInput{Model: "seedance-2.5", ChannelID: 292})
	require.NoError(t, err)
	phase = "failure"
	require.NoError(t, RefreshSeedanceVerification(c, v))
	require.Equal(t, "failed", v.Status)
	require.Empty(t, v.UpstreamID)
	c.Request.Header.Set("Idempotency-Key", "gateway-session-3")
	v, err = CreateSeedanceVerification(c, SeedanceVerificationInput{Model: "seedance-2.5", ChannelID: 292})
	require.NoError(t, err)
	v.ExpiresAt = time.Now().Unix() - 1
	require.NoError(t, RefreshSeedanceVerification(c, v))
	require.Equal(t, "expired", v.Status)
}
