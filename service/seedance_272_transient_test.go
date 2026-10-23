package service

import (
	"context"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestChannel272ManualReviewDoesNotAllocateAndOriginalIDsRemainUsable(t *testing.T) {
	db, c := unifiedRoutingFixture(t)
	base := "https://ark.cn-beijing.volces.com"
	ch := model.Channel{Id: 272, Type: constant.ChannelTypeDoubaoVideo, BaseURL: &base, Key: "official-key", Status: 1, Models: "seedance-2.0-fast", Group: "default"}
	require.NoError(t, db.Create(&ch).Error)
	require.NoError(t, db.Create(&model.Ability{ChannelId: 272, Model: "seedance-2.0-fast", Group: "default", Enabled: true}).Error)
	require.NoError(t, db.Model(&model.SeedanceResource{}).Where("id = ?", "logical-a").Updates(map[string]any{"asset_type": "Image", "source_url": "https://example.com/original.png"}).Error)
	model.InitChannelCache()
	plan, err := unifiedSeedanceCurrentPlan(c)
	require.NoError(t, err)
	for _, ids := range plan.Models {
		require.NotContains(t, ids, 272)
	}
	common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, "272")
	_, err = CreateSeedanceGroup(c, "seedance-2.0-fast", SeedanceGroupInput{ChannelID: 272})
	require.ErrorContains(t, err, "temporary automatic review")
	_, err = SubmitSeedanceAssets(c, SeedanceAssetSubmission{Model: "seedance-2.0-fast", Assets: []SeedanceAssetInput{{URL: "https://example.com/original.png"}}})
	require.ErrorContains(t, err, "temporary automatic review")
	fields := map[string]any{"model": "seedance-2.0-fast", "image_urls": []any{"asset://logical-a"}, "generate_audio": false, "seed": 0}
	route, err := SeedanceAssetRouting(c, fields)
	require.NoError(t, err)
	require.Equal(t, 272, route.ChannelID)
	require.Empty(t, route.KeyFingerprint)
	out, err := ResolveSeedanceAssetReferences(c, fields, 272, "official-key")
	require.NoError(t, err)
	require.Equal(t, []any{"https://example.com/original.png"}, out["image_urls"])
	require.Equal(t, false, out["generate_audio"])
	require.Equal(t, 0, out["seed"])
	require.Equal(t, []any{"asset://logical-a"}, fields["image_urls"])
	var n int64
	require.NoError(t, db.Model(&model.SeedanceResource{}).Where("channel_id = ?", 272).Count(&n).Error)
	require.Zero(t, n)
	c.Set("id", 2)
	_, err = SeedanceAssetRouting(c, fields)
	require.Error(t, err)
}
func TestChannel272RejectsUnusableSourcesAndForeignApprovals(t *testing.T) {
	db, c := unifiedRoutingFixture(t)
	for _, entry := range []struct{ name, kind, status, source string }{
		{"not-ready", "Image", "Pending", "https://example.com/a.png"},
		{"video", "Video", "Active", "https://example.com/a.mp4"},
		{"missing-source", "Image", "Active", ""},
		{"private-source", "Image", "Active", "http://127.0.0.1/a.png"},
	} {
		t.Run(entry.name, func(t *testing.T) {
			require.NoError(t, db.Model(&model.SeedanceResource{}).Where("id = ?", "logical-a").Updates(map[string]any{"asset_type": entry.kind, "status": entry.status, "source_url": entry.source}).Error)
			_, err := seedance272OriginalImage(c, "logical-a")
			require.Error(t, err)
		})
	}
	require.NoError(t, db.Create(&model.SeedanceResource{ID: "portrait", Kind: "asset", UserID: 1, ChannelID: 273, AssetType: "Image", GroupType: "channel_portrait", Status: "Active", UpstreamID: "foreign", SourceURL: "https://example.com/a.png"}).Error)
	_, err := seedance272OriginalImage(c, "portrait")
	require.Error(t, err)
	_, err = seedance272OriginalImage(c, "private-upstream-id")
	require.Error(t, err)
}

func TestChannel272BridgeCleanupKeepsOriginalMaterial(t *testing.T) {
	db := automaticReviewTestLibrary(t, func(action string, fields map[string]any) (int, string) {
		switch action {
		case "CreateAssetGroup":
			return 200, `{"Result":{"Id":"temporary-group"}}`
		case "CreateAsset":
			require.Equal(t, "https://example.com/original.png", fields["URL"])
			return 200, `{"Result":{"Id":"temporary-image"}}`
		case "GetAsset":
			return 200, `{"Result":{"Status":"Active"}}`
		case "DeleteAsset", "DeleteAssetGroup":
			return 200, `{"Result":{}}`
		default:
			t.Fatalf("unexpected %s", action)
			return 500, `{}`
		}
	})
	require.NoError(t, db.Create(&model.SeedanceResource{ID: "user-original", Kind: "asset", UserID: 1, ChannelID: 292, GroupType: "virtual", Status: "Active", AssetType: "Image", UpstreamID: "retained-provider-image", SourceURL: "https://example.com/original.png"}).Error)
	c := seedanceContext(1)
	fields := map[string]any{"content": []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "asset://user-original"}}}}
	out, err := ResolveSeedanceAssetReferences(c, fields, 272, "inference-key")
	require.NoError(t, err)
	raw, err := common.Marshal(out)
	require.NoError(t, err)
	reviewed, err := PrepareAutomaticSeedanceReview(c, "seedance-2.0-mini", "inference-key", "task-bridge", raw)
	require.NoError(t, err)
	require.Contains(t, string(reviewed), "asset://temporary-image")
	require.NoError(t, MarkAutomaticSeedanceReviewSending(1, "task-bridge", "upstream-video"))
	require.NoError(t, db.Create(&model.Task{TaskID: "task-bridge", UserId: 1, ChannelId: 272, Status: model.TaskStatusInProgress}).Error)
	require.NoError(t, CleanupAutomaticSeedanceReviews(context.Background()))
	var n int64
	require.NoError(t, db.Model(&model.SeedanceResource{}).Where("channel_id = ? AND kind IN ?", 272, []string{"asset", "group"}).Count(&n).Error)
	require.EqualValues(t, 2, n)
	require.NoError(t, db.Model(&model.Task{}).Where("task_id = ?", "task-bridge").Update("status", model.TaskStatusSuccess).Error)
	require.NoError(t, CleanupAutomaticSeedanceReviews(context.Background()))
	require.NoError(t, db.Model(&model.SeedanceResource{}).Where("channel_id = ? AND kind IN ?", 272, []string{"asset", "group"}).Count(&n).Error)
	require.Zero(t, n)
	original, err := model.GetSeedanceResource(1, "asset", "user-original")
	require.NoError(t, err)
	require.Equal(t, "https://example.com/original.png", original.SourceURL)
	require.Equal(t, "Active", original.Status)
}

func TestChannel272RetiredReplicaDoesNotBlockManualReadiness(t *testing.T) {
	db, _ := unifiedRoutingFixture(t)
	require.NoError(t, db.Model(&model.SeedanceResource{}).Where("id = ?", "logical-a").Update("request_data", `{"models":{"seedance-2.0-fast":[7,272]}}`).Error)
	asset, err := model.GetSeedanceResource(1, "asset", "logical-a")
	require.NoError(t, err)
	dto := unifiedSeedanceAssetDTO(asset)
	require.Equal(t, true, dto["review_complete"])
	require.Contains(t, dto["available_models"], "seedance-2.0-fast")
}

type seedance272TestTransport func(*http.Request) (*http.Response, error)

func (f seedance272TestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestChannel272LocalFailureDoesNotDeleteGeneratingUpstreamMaterial(t *testing.T) {
	deleted := 0
	db := automaticReviewTestLibrary(t, func(action string, fields map[string]any) (int, string) {
		switch action {
		case "CreateAssetGroup":
			return 200, `{"Result":{"Id":"temp-group"}}`
		case "CreateAsset":
			return 200, `{"Result":{"Id":"temp-image"}}`
		case "GetAsset":
			return 200, `{"Result":{"Status":"Active"}}`
		case "DeleteAsset", "DeleteAssetGroup":
			deleted++
			return 200, `{"Result":{}}`
		default:
			t.Fatalf("unexpected %s", action)
			return 500, `{}`
		}
	})
	c := seedanceContext(1)
	_, err := PrepareAutomaticSeedanceReview(c, "seedance-2.0-mini", "inference-key", "task-local-failure", []byte(automaticReviewNativeRequest))
	require.NoError(t, err)
	require.NoError(t, MarkAutomaticSeedanceReviewSending(1, "task-local-failure", "upstream-running"))
	require.NoError(t, db.Create(&model.Task{TaskID: "task-local-failure", UserId: 1, ChannelId: 272, Status: model.TaskStatusFailure}).Error)
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	state := "processing"
	http.DefaultTransport = seedance272TestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "ark.cn-beijing.volces.com" {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"` + state + `"}`)), Header: make(http.Header)}, nil
		}
		return original.RoundTrip(r)
	})
	require.NoError(t, CleanupAutomaticSeedanceReviews(context.Background()))
	require.Zero(t, deleted)
	state = "failed"
	require.NoError(t, CleanupAutomaticSeedanceReviews(context.Background()))
	require.Equal(t, 2, deleted)
}
func TestChannel272AutomaticReviewDoesNotReplayAcceptedVideo(t *testing.T) {
	require.False(t, ShouldAutomaticallyReviewSeedance(272, 400, "seedance-2.0-mini", []byte(`{"id":"already-accepted","error":{"code":"InputImageSensitiveContentDetected.PrivacyInformation"}}`)))
	require.False(t, ShouldAutomaticallyReviewSeedance(272, 400, "seedance-2.0-mini", []byte(`{"data":{"task_id":"already-accepted"},"error":{"code":"InputImageSensitiveContentDetected.PrivacyInformation"}}`)))
}
