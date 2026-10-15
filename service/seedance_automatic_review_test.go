package service

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func automaticReviewTestLibrary(t *testing.T, handle func(string, map[string]any) (int, string)) *gorm.DB {
	db := seedanceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Task{}))
	dir := t.TempDir()
	t.Setenv("BYTEPLUS_ASSET_CREDENTIALS_DIR", dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "channel-272.json"), []byte(`{"access_key_id":"test-ak","secret_access_key":"test-sk","region":"cn-beijing","project_name":"default","auto_route_enabled":false}`), 0600))
	base := "https://ark.cn-beijing.volces.com"
	require.NoError(t, db.Create(&model.Channel{Id: 272, Type: constant.ChannelTypeDoubaoVideo, Key: "inference-key", BaseURL: &base, Status: 1, Models: "seedance-2.0-mini", Group: "default"}).Error)
	model.InitChannelCache()
	original := http.DefaultTransport
	http.DefaultTransport = xingchiyunTestTransport(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "ark.cn-beijing.volcengineapi.com", r.URL.Host)
		require.Contains(t, r.Header.Get("Authorization"), "Credential=test-ak/")
		require.NotContains(t, r.Header.Get("Authorization"), "inference-key")
		var fields map[string]any
		require.NoError(t, common.DecodeJson(r.Body, &fields))
		require.Equal(t, "default", fields["ProjectName"])
		require.NotContains(t, fields, "Moderation")
		action := r.URL.Query().Get("Action")
		require.NotContains(t, action, "VisualValidate")
		status, body := handle(action, fields)
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	return db
}

const automaticReviewNativeRequest = `{"model":"doubao-seedance-2-0-mini-260615","content":[{"type":"text","text":"A person blinks"},{"type":"image_url","role":"reference_image","image_url":{"url":"https://example.com/photo.jpg"}}],"duration":4,"resolution":"480p","ratio":"adaptive","generate_audio":false,"watermark":false,"seed":0}`

func TestAutomaticReviewTriggerIsRestrictedToExplicitFaceRejection(t *testing.T) {
	raw := []byte(`{"error":{"code":"InputImageSensitiveContentDetected.PrivacyInformation"}}`)
	require.True(t, ShouldAutomaticallyReviewSeedance(272, 400, "seedance-2.0-mini", raw))
	require.False(t, ShouldAutomaticallyReviewSeedance(292, 400, "seedance-2.0-mini", raw))
	require.False(t, ShouldAutomaticallyReviewSeedance(272, 500, "seedance-2.0-mini", raw))
	require.False(t, ShouldAutomaticallyReviewSeedance(272, 400, "other", raw))
	require.False(t, ShouldAutomaticallyReviewSeedance(272, 400, "seedance-2.0-mini", []byte(`{"error":{"code":"InputImageSensitiveContentDetected.PolicyViolation"}}`)))
}

func TestAutomaticReviewPreservesRequestAndDeletesOnlyAfterVideoTerminal(t *testing.T) {
	for _, terminal := range []model.TaskStatus{model.TaskStatusSuccess, model.TaskStatusFailure} {
		t.Run(string(terminal), func(t *testing.T) {
			var calls []string
			deleteAttempts := 0
			db := automaticReviewTestLibrary(t, func(action string, f map[string]any) (int, string) {
				calls = append(calls, action)
				switch action {
				case "CreateAssetGroup":
					require.Equal(t, "AIGC", f["GroupType"])
					return 200, `{"Result":{"Id":"native-group"}}`
				case "CreateAsset":
					require.Equal(t, "native-group", f["GroupId"])
					return 200, `{"Result":{"Id":"native-image"}}`
				case "GetAsset":
					return 200, `{"Result":{"Status":"Active"}}`
				case "DeleteAsset":
					deleteAttempts++
					if deleteAttempts == 1 {
						return 503, `{}`
					}
					return 200, `{"Result":{}}`
				case "DeleteAssetGroup":
					require.GreaterOrEqual(t, deleteAttempts, 2)
					return 200, `{"Result":{}}`
				default:
					t.Fatalf("unexpected action %s", action)
					return 500, `{}`
				}
			})
			c := seedanceContext(1)
			raw, err := PrepareAutomaticSeedanceReview(c, "seedance-2.0-mini", "inference-key", "task_test", []byte(automaticReviewNativeRequest))
			require.NoError(t, err)
			var out, original map[string]any
			require.NoError(t, common.Unmarshal(raw, &out))
			require.NoError(t, common.Unmarshal([]byte(automaticReviewNativeRequest), &original))
			outContent := out["content"].([]any)
			item := outContent[1].(map[string]any)
			require.Equal(t, map[string]any{"url": "asset://native-image"}, item["image_url"])
			item["image_url"] = map[string]any{"url": "https://example.com/photo.jpg"}
			require.Equal(t, original, out)
			require.NoError(t, db.Create(&model.Task{TaskID: "task_test", UserId: 1, ChannelId: 272, Status: model.TaskStatusInProgress}).Error)
			require.NoError(t, CleanupAutomaticSeedanceReviews(context.Background()))
			require.NotContains(t, calls, "DeleteAsset")
			require.NoError(t, db.Model(&model.Task{}).Where("task_id = ?", "task_test").Update("status", terminal).Error)
			require.Error(t, CleanupAutomaticSeedanceReviews(context.Background()))
			require.NotContains(t, calls, "DeleteAssetGroup")
			require.NoError(t, CleanupAutomaticSeedanceReviews(context.Background()))
			require.Equal(t, []string{"CreateAssetGroup", "CreateAsset", "GetAsset", "DeleteAsset", "DeleteAsset", "DeleteAssetGroup"}, calls)
			var count int64
			require.NoError(t, db.Model(&model.SeedanceResource{}).Count(&count).Error)
			require.Zero(t, count)
			require.NoError(t, CleanupAutomaticSeedanceReviews(context.Background()))
			require.Equal(t, 2, deleteAttempts)
		})
	}
}

func TestAutomaticReviewFailureKeepsMessageAndQueuesCleanup(t *testing.T) {
	var deleted []string
	automaticReviewTestLibrary(t, func(action string, f map[string]any) (int, string) {
		switch action {
		case "CreateAssetGroup":
			return 200, `{"Result":{"Id":"native-group"}}`
		case "CreateAsset":
			return 200, `{"Result":{"Id":"native-image"}}`
		case "GetAsset":
			return 200, `{"Result":{"Status":"Failed","Error":{"Code":"ImageRejected","Message":"Face was rejected; test-sk https://example.com/photo.jpg"}}}`
		case "DeleteAsset", "DeleteAssetGroup":
			deleted = append(deleted, action)
			return 200, `{"Result":{}}`
		}
		return 500, `{}`
	})
	_, err := PrepareAutomaticSeedanceReview(seedanceContext(1), "seedance-2.0-mini", "inference-key", "task_failed", []byte(automaticReviewNativeRequest))
	require.ErrorContains(t, err, "Face was rejected")
	require.NotContains(t, err.Error(), "test-sk")
	require.NotContains(t, err.Error(), "https://example.com")
	require.Equal(t, "ImageRejected", err.(*SeedanceAPIError).Code)
	require.NoError(t, CleanupAutomaticSeedanceReviews(context.Background()))
	require.Equal(t, []string{"DeleteAsset", "DeleteAssetGroup"}, deleted)
}

func TestAutomaticReviewClientImportErrorAndNoDuplicatePOST(t *testing.T) {
	creates := 0
	automaticReviewTestLibrary(t, func(action string, f map[string]any) (int, string) {
		switch action {
		case "CreateAssetGroup":
			creates++
			return 200, `{"Result":{"Id":"native-group"}}`
		case "CreateAsset":
			creates++
			return 400, `{"ResponseMetadata":{"Error":{"Code":"InvalidImage","Message":"Image is too small"}}}`
		case "DeleteAssetGroup":
			return 404, `{"ResponseMetadata":{"Error":{"Code":"NotFound","Message":"Already deleted"}}}`
		default:
			t.Fatalf("unexpected %s", action)
			return 500, "{}"
		}
	})
	_, err := PrepareAutomaticSeedanceReview(seedanceContext(1), "seedance-2.0-mini", "inference-key", "task_failed", []byte(automaticReviewNativeRequest))
	require.ErrorContains(t, err, "Image is too small")
	require.Equal(t, "InvalidImage", err.(*SeedanceAPIError).Code)
	_, err = PrepareAutomaticSeedanceReview(seedanceContext(1), "seedance-2.0-mini", "inference-key", "task_failed", []byte(automaticReviewNativeRequest))
	require.Error(t, err)
	require.Equal(t, 2, creates)
	require.NoError(t, CleanupAutomaticSeedanceReviews(context.Background()))
}

func TestAutomaticReviewUncertainImportIsRecoveredForCleanupWithoutResubmission(t *testing.T) {
	creates := 0
	assetName := ""
	var deleted []string
	automaticReviewTestLibrary(t, func(action string, f map[string]any) (int, string) {
		switch action {
		case "CreateAssetGroup":
			return 200, `{"Result":{"Id":"native-group"}}`
		case "CreateAsset":
			creates++
			assetName = f["Name"].(string)
			return 503, `{}`
		case "ListAssets":
			require.Equal(t, assetName, f["Filter"].(map[string]any)["Name"])
			return 200, `{"Result":{"Items":[{"Name":"` + assetName + `","Id":"recovered-image"}]}}`
		case "DeleteAsset":
			require.Equal(t, "recovered-image", f["Id"])
			deleted = append(deleted, action)
			return 200, `{"Result":{}}`
		case "DeleteAssetGroup":
			deleted = append(deleted, action)
			return 200, `{"Result":{}}`
		default:
			t.Fatalf("unexpected %s", action)
			return 500, `{}`
		}
	})
	_, err := PrepareAutomaticSeedanceReview(seedanceContext(1), "seedance-2.0-mini", "inference-key", "task_unknown", []byte(automaticReviewNativeRequest))
	require.Error(t, err)
	require.NoError(t, CleanupAutomaticSeedanceReviews(context.Background()))
	require.Equal(t, 1, creates)
	require.Equal(t, []string{"DeleteAsset", "DeleteAssetGroup"}, deleted)
}

func TestAutomaticReviewFailureMessageSurvivesPublicResponseRedaction(t *testing.T) {
	err := PublicVideoSubmitError(400, []byte(`{"error":{"code":"InvalidImage","message":"Reference image width must be at least 300 pixels; api_key=sk-secret"}}`), "sk-secret", 272)
	require.Contains(t, err.Message, "Reference image width must be at least 300 pixels")
	require.NotContains(t, err.Message, "sk-secret")
}
