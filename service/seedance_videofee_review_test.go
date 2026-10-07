package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestVideoFeePurposeAndModelScope(t *testing.T) {
	db := seedanceTestDB(t)
	posts := 0
	purposes := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			require.Contains(t, []string{"/api/assets/import-url", "/api/real-person-assets/import-url"}, r.URL.Path)
			posts++
			id := fmt.Sprintf("provider%d", posts)
			purpose := "ordinary"
			if strings.Contains(r.URL.Path, "real-person") {
				purpose = "real_person"
			}
			purposes[id] = purpose
			fmt.Fprintf(w, `{"row":{"assetKey":%q,"purpose":%q,"status":"certifying","assetCertification":{"status":"certifying"}}}`, id, purpose)
		} else {
			id := strings.TrimPrefix(r.URL.Path, "/api/assets/")
			fmt.Fprintf(w, `{"row":{"purpose":%q,"status":"active","certifications":[{"interfaceCode":"route_private","status":"active","assetId":"never-public","errorMessage":"never-public"}]}}`, purposes[id])
		}
	}))
	defer server.Close()
	base := server.URL
	key := "test-key"
	require.NoError(t, db.Create(&model.Channel{Id: 273, Key: key, BaseURL: &base, Status: 1, Models: "seedance-2.0,seedance-2.5", Group: "default"}).Error)
	for _, name := range []string{"seedance-2.0", "seedance-2.5"} {
		require.NoError(t, db.Create(&model.Ability{ChannelId: 273, Model: name, Group: "default", Enabled: true}).Error)
	}
	model.InitChannelCache()
	c := seedanceContext(1)
	cap, err := SeedancePortraitCapabilities(c, "seedance-2.5")
	require.NoError(t, err)
	require.Len(t, cap, 1)
	require.Equal(t, "channel_material_review", cap[0]["method"])
	require.Equal(t, false, cap[0]["hosted_callback"])
	for _, purpose := range []string{"ordinary", "channel_portrait"} {
		group, err := CreateSeedanceGroup(c, "seedance-2.5", SeedanceGroupInput{Purpose: purpose, ChannelID: 273})
		require.NoError(t, err)
		require.Equal(t, purpose, group.GroupType)
		require.Empty(t, group.VerificationID)
		input := SeedanceAssetSubmission{Model: "seedance-2.5", GroupID: group.ID, Assets: []SeedanceAssetInput{{URL: "https://example.com/image.png"}}}
		c.Request.Header.Set("Idempotency-Key", "purpose-"+purpose)
		before := posts
		task, err := SubmitSeedanceAssets(c, input)
		require.NoError(t, err)
		again, err := SubmitSeedanceAssets(c, input)
		require.NoError(t, err)
		require.Equal(t, task.ID, again.ID)
		require.Equal(t, before+1, posts)
		require.NoError(t, PollSeedanceAssetTask(c, task))
		require.Equal(t, "completed", task.Status)
		var ids []string
		require.NoError(t, common.UnmarshalJsonStr(task.RequestData, &ids))
		a, err := model.GetSeedanceResource(1, "asset", ids[0])
		require.NoError(t, err)
		require.Equal(t, "Active", a.Status)
		dto := SeedanceAssetDTO(a)
		require.Equal(t, false, dto["owner_verified"])
		raw, err := common.Marshal(dto)
		require.NoError(t, err)
		require.NotContains(t, string(raw), "route_private")
		require.NotContains(t, string(raw), "never-public")
		if purpose == "channel_portrait" {
			_, err = SeedanceAssetRouting(c, map[string]any{"model": "seedance-2.0", "image_urls": []any{"asset://" + a.ID}})
			require.ErrorContains(t, err, "compatibility")
			_, err = SeedanceAssetRouting(c, map[string]any{"model": "seedance-2.5", "image_urls": []any{"asset://" + a.ID}})
			require.NoError(t, err)
			c.Request.Header.Del("Idempotency-Key")
			input.Assets[0].URL = "https://example.com/next.png"
			_, err = SubmitSeedanceAssets(c, input)
			require.ErrorContains(t, err, "Idempotency-Key")
			require.Equal(t, before+1, posts)
		}
	}
	c2 := seedanceContext(2)
	common.SetContextKey(c2, constant.ContextKeyTokenSpecificChannelId, "272")
	_, err = CreateSeedanceGroup(c2, "seedance-2.5", SeedanceGroupInput{Purpose: "channel_portrait", ChannelID: 273})
	require.ErrorContains(t, err, "conflicts")
}

func TestVideoFeeStatusNeverTreatsUploadAsPortraitApproval(t *testing.T) {
	portrait := &model.SeedanceResource{GroupType: "channel_portrait"}
	cases := []struct {
		body string
		want string
	}{
		{`{"status":"certifying","uploadStatus":"uploaded"}`, "Pending"},
		{`{"status":"uploaded"}`, "Pending"},
		{`{"status":"active","purpose":"ordinary"}`, "Failed"},
		{`{"status":"blocked"}`, "Failed"},
		{`{"assetCertification":{"status":"active"}}`, "Active"},
		{`{"certifications":[{"status":"active"},{"status":"certifying"}]}`, "Pending"},
		{`{"certifications":[{"status":"active"},{"status":"failed"}]}`, "Failed"},
		{`{"certifications":[{"status":"active"}]}`, "Active"},
	}
	for _, tc := range cases {
		var row map[string]any
		require.NoError(t, common.UnmarshalJsonStr(tc.body, &row))
		status, err := videoFeeReviewedStatus(portrait, row)
		require.NoError(t, err)
		require.Equal(t, tc.want, status)
	}
	status, err := videoFeeReviewedStatus(&model.SeedanceResource{GroupType: "ordinary"}, map[string]any{"status": "uploaded"})
	require.NoError(t, err)
	require.Equal(t, "Active", status)
}

func TestVideoFeePrivateUploadOwnerAndSignature(t *testing.T) {
	dir := t.TempDir()
	key := strings.Repeat("k", 32)
	origin := "https://apimaster.example"
	t.Setenv("SEEDANCE_PORTRAIT_DIR", dir)
	t.Setenv("SEEDANCE_PORTRAIT_SIGNING_KEY", key)
	t.Setenv("SEEDANCE_CALLBACK_ORIGIN", origin)
	id := "1_" + strings.Repeat("a", 48)
	data := []byte("\x89PNG\r\n\x1a\nfixture")
	require.NoError(t, os.WriteFile(filepath.Join(dir, id), data, 0600))
	expiry := strconv.FormatInt(time.Now().Unix()+600, 10)
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(id + ":" + expiry))
	sig := hex.EncodeToString(mac.Sum(nil))
	a := &model.SeedanceResource{UserID: 1, SourceURL: origin + "/v1/seedance2/private-avatar/files/" + id + "/" + expiry + "/" + sig}
	upload, err := videoFeePrivateUpload(a)
	require.NoError(t, err)
	require.Equal(t, data, upload.data)
	a.UserID = 2
	_, err = videoFeePrivateUpload(a)
	require.ErrorContains(t, err, "belong")
	a.UserID = 1
	a.SourceURL += "bad"
	_, err = videoFeePrivateUpload(a)
	require.ErrorContains(t, err, "invalid")
	a.SourceURL = "https://external.example/photo.jpg"
	upload, err = videoFeePrivateUpload(a)
	require.NoError(t, err)
	require.Nil(t, upload)
}
