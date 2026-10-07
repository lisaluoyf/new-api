package service

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSeedanceHostedCallbackAndFourModels(t *testing.T) {
	for _, name := range []string{"seedance-2.0", "seedance-2.0-fast", "seedance-2.0-mini", "seedance-2.5"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("SEEDANCE_CALLBACK_ORIGIN", "https://apimaster.example")
			var callback string
			verified, temporary := false, false
			creates, queries := 0, 0
			db := bytePlusTestLibrary(t, func(w http.ResponseWriter, r *http.Request) {
				action, fields := bytePlusTestRequest(t, r)
				switch action {
				case "CreateVisualValidateSession":
					creates++
					callback = fields["CallbackURL"].(string)
					fmt.Fprint(w, `{"Result":{"BytedToken":"private-token","H5Link":"https://official.example/h5?sessionToken=temporary"}}`)
				case "GetVisualValidateResult":
					queries++
					if temporary {
						w.WriteHeader(503)
						fmt.Fprint(w, `{"error":"temporary"}`)
					} else if verified {
						fmt.Fprint(w, `{"Result":{"GroupId":"private-group"}}`)
					} else {
						fmt.Fprint(w, `{"Result":{}}`)
					}
				default:
					t.Fatalf("unexpected action %s", action)
				}
			})
			if name != "seedance-2.5" {
				require.NoError(t, db.Create(&model.Ability{ChannelId: 283, Model: name, Group: "default", Enabled: true}).Error)
			}
			model.InitChannelCache()
			c := seedanceContext(1)
			c.Request.Header.Set("Idempotency-Key", "create-once")
			input := SeedanceVerificationInput{Model: name, ChannelID: 283}
			v, err := CreateSeedanceVerification(c, input)
			require.NoError(t, err)
			require.Equal(t, "test-project", v.ProjectName)
			require.NotEmpty(t, v.CredentialFingerprint)
			again, err := CreateSeedanceVerification(c, input)
			require.NoError(t, err)
			require.Equal(t, v.ID, again.ID)
			require.Equal(t, 1, creates)
			input.Name = "different"
			_, err = CreateSeedanceVerification(c, input)
			require.ErrorContains(t, err, "different request")
			parsed, err := url.Parse(callback)
			require.NoError(t, err)
			state := parsed.Path[strings.LastIndex(parsed.Path, "/")+1:]
			require.Error(t, HandleSeedanceVerificationCallback(c, v.ID, "forged", "private-token", "10000"))
			require.Error(t, HandleSeedanceVerificationCallback(c, v.ID, state, "wrong-token", "10000"))
			require.Zero(t, queries)
			require.NoError(t, HandleSeedanceVerificationCallback(c, v.ID, state, "private-token", "10000"))
			persisted, err := model.GetSeedanceResource(1, "verification", v.ID)
			require.NoError(t, err)
			require.NotEqual(t, "completed", persisted.Status)
			require.NotContains(t, SeedanceVerificationDTO(persisted), "verification_url")
			temporary = true
			require.Error(t, RefreshSeedanceVerification(c, persisted))
			require.Equal(t, "pending", persisted.Status)
			temporary = false
			verified = true
			require.NoError(t, HandleSeedanceVerificationCallback(c, v.ID, state, "private-token", "10000"))
			require.NoError(t, HandleSeedanceVerificationCallback(c, v.ID, state, "private-token", "10000"))
			persisted, err = model.GetSeedanceResource(1, "verification", v.ID)
			require.NoError(t, err)
			require.Equal(t, "completed", persisted.Status)
			var count int64
			require.NoError(t, db.Model(&model.SeedanceResource{}).Where("kind = ?", "group").Count(&count).Error)
			require.EqualValues(t, 1, count)
			group, err := model.GetSeedanceResource(1, "group", persisted.GroupID)
			require.NoError(t, err)
			require.Equal(t, "real_person", group.GroupType)
			require.Equal(t, v.ID, group.VerificationID)
			_, err = model.GetSeedanceResource(2, "group", group.ID)
			require.Error(t, err)
			public, _ := common.Marshal(SeedanceVerificationDTO(persisted))
			for _, secret := range []string{"private-token", "private-group", "test-ak", "test-sk", "temporary"} {
				require.NotContains(t, string(public), secret)
			}
		})
	}
}
func TestSeedanceVerificationExpiryUnsupportedAndUncertain(t *testing.T) {
	t.Setenv("SEEDANCE_CALLBACK_ORIGIN", "https://apimaster.example")
	calls := 0
	db := bytePlusTestLibrary(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(504)
		fmt.Fprint(w, `{"error":"timeout"}`)
	})
	c := seedanceContext(1)
	c.Request.Header.Set("Idempotency-Key", "uncertain-once")
	input := SeedanceVerificationInput{Model: "seedance-2.5"}
	v, err := CreateSeedanceVerification(c, input)
	require.NoError(t, err)
	require.Equal(t, "submission_unknown", v.Status)
	_, err = CreateSeedanceVerification(c, input)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	v.ExpiresAt = time.Now().Unix() - 1
	require.NoError(t, RefreshSeedanceVerification(c, v))
	require.Equal(t, "expired", v.Status)
	c.Request.Header.Set("Idempotency-Key", "unsupported-once")
	common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, "999")
	_, err = CreateSeedanceVerification(c, input)
	require.ErrorContains(t, err, "does not support")
	require.Equal(t, 1, calls)
	_, err = CreateSeedanceVerification(c, SeedanceVerificationInput{Model: "seedance-1.5"})
	require.Error(t, err)
	var count int64
	require.NoError(t, db.Model(&model.SeedanceResource{}).Where("kind = ?", "group").Count(&count).Error)
	require.Zero(t, count)
}
func TestSeedanceAssetUncertainBatchPreservesAcceptedAsset(t *testing.T) {
	calls := 0
	db := bytePlusTestLibrary(t, func(w http.ResponseWriter, r *http.Request) {
		action, _ := bytePlusTestRequest(t, r)
		switch action {
		case "CreateAssetGroup":
			fmt.Fprint(w, `{"Result":{"Id":"private-group"}}`)
		case "CreateAsset":
			calls++
			if calls == 1 {
				fmt.Fprint(w, `{"Result":{"Id":"accepted-one"}}`)
			} else {
				w.WriteHeader(504)
				fmt.Fprint(w, `{"error":"timeout"}`)
			}
		case "GetAsset":
			fmt.Fprint(w, `{"Result":{"Status":"Active"}}`)
		default:
			t.Fatalf("unexpected %s", action)
		}
	})
	c := seedanceContext(1)
	c.Request.Header.Set("Idempotency-Key", "asset-batch-once")
	input := SeedanceAssetSubmission{Model: "seedance-2.5", Assets: []SeedanceAssetInput{{URL: "https://approved.example/a.png"}, {URL: "https://approved.example/b.png"}}}
	task, err := SubmitSeedanceAssets(c, input)
	require.NoError(t, err)
	require.Equal(t, "submission_unknown", task.Status)
	again, err := SubmitSeedanceAssets(c, input)
	require.NoError(t, err)
	require.Equal(t, task.ID, again.ID)
	require.Equal(t, 2, calls)
	require.NoError(t, PollSeedanceAssetTask(c, task))
	var assets []model.SeedanceResource
	require.NoError(t, db.Where("kind = ? AND group_id = ?", "asset", task.GroupID).Find(&assets).Error)
	active, unknown := 0, 0
	for _, asset := range assets {
		if asset.Status == "Active" {
			active++
		}
		if asset.UpstreamID == "" {
			unknown++
		}
	}
	require.Equal(t, 1, active)
	require.Equal(t, 1, unknown)
	require.Equal(t, 2, calls)
}

func TestSeedancePortraitVideoDeduplicationAndProjectIsolation(t *testing.T) {
	db := bytePlusTestLibrary(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("No upstream request expected") })
	credentials, e := loadBytePlusAssetCredentials(283)
	require.NoError(t, e)
	asset := &model.SeedanceResource{ID: "asset_real", Kind: "asset", UserID: 1, ChannelID: 283, KeyFingerprint: SeedanceKeyFingerprint("inference-key"), ProjectName: credentials.ProjectName, CredentialFingerprint: seedanceControlFingerprint(credentials), Status: "Active", UpstreamID: "private-asset"}
	c := seedanceContext(1)
	c.Request.Header.Set("Idempotency-Key", "video-once")
	fields := map[string]any{"model": "seedance-2.5", "image_urls": []any{"asset://asset_real"}, "duration": 4, "generate_audio": false}
	r, fresh, e := BeginSeedancePortraitVideo(c, fields, asset)
	require.NoError(t, e)
	require.True(t, fresh)
	again, fresh, e := BeginSeedancePortraitVideo(c, fields, asset)
	require.NoError(t, e)
	require.False(t, fresh)
	require.Equal(t, r.UpstreamID, again.UpstreamID)
	fields["duration"] = 5
	_, _, e = BeginSeedancePortraitVideo(c, fields, asset)
	require.ErrorContains(t, e, "different video")
	_, e = model.GetSeedanceResource(2, "video_request", r.ID)
	require.Error(t, e)
	require.NoError(t, db.Create(asset).Error)
	asset.ProjectName = "other-project"
	asset.ID = "asset_other"
	require.NoError(t, db.Create(asset).Error)
	_, e = SeedanceAssetRouting(c, map[string]any{"model": "seedance-2.5", "image_urls": []any{"asset://asset_real", "asset://asset_other"}})
	require.ErrorContains(t, e, "same media library")
	_, e = SeedanceAssetRouting(c, map[string]any{"model": "seedance-2.5", "image_urls": []any{"asset://asset_other"}})
	require.ErrorContains(t, e, "incompatible")
}

func TestSeedanceVerificationFailureRateLimitAndNoCredentialRetention(t *testing.T) {
	t.Setenv("SEEDANCE_CALLBACK_ORIGIN", "https://apimaster.example")
	subscription := false
	db := bytePlusTestLibrary(t, func(w http.ResponseWriter, r *http.Request) {
		action, _ := bytePlusTestRequest(t, r)
		if action == "CreateVisualValidateSession" {
			if subscription {
				fmt.Fprint(w, `{"ResponseMetadata":{"Error":{"Code":"SubscriptionRequired","Message":"sensitive"}}}`)
			} else {
				fmt.Fprint(w, `{"Result":{"BytedToken":"private-token","H5Link":"https://official.example/h5"}}`)
			}
		} else {
			fmt.Fprint(w, `{"Result":{}}`)
		}
	})
	for i := 0; i < 3; i++ {
		c := seedanceContext(1)
		c.Request.Header.Set("Idempotency-Key", fmt.Sprintf("rate-limit-%d", i))
		_, e := CreateSeedanceVerification(c, SeedanceVerificationInput{Model: "seedance-2.5"})
		require.NoError(t, e)
	}
	c := seedanceContext(1)
	c.Request.Header.Set("Idempotency-Key", "rate-limit-extra")
	_, e := CreateSeedanceVerification(c, SeedanceVerificationInput{Model: "seedance-2.5"})
	require.ErrorContains(t, e, "Too many")
	var existing model.SeedanceResource
	require.NoError(t, db.Where("kind = ?", "verification").First(&existing).Error)
	// A matched failure is a hint and must not create a real group.
	state := "test-state"
	existing.CallbackHash = SeedanceKeyFingerprint(state)
	require.NoError(t, db.Save(&existing).Error)
	require.NoError(t, HandleSeedanceVerificationCallback(c, existing.ID, state, "private-token", "20000"))
	persisted, e := model.GetSeedanceResource(1, "verification", existing.ID)
	require.NoError(t, e)
	require.Equal(t, "failed_unconfirmed", persisted.Status)
	persisted.ExpiresAt = time.Now().Unix() - 1
	require.NoError(t, RefreshSeedanceVerification(c, persisted))
	persisted, e = model.GetSeedanceResource(1, "verification", existing.ID)
	require.NoError(t, e)
	require.Equal(t, "expired", persisted.Status)
	require.Empty(t, persisted.UpstreamID)
	require.Empty(t, persisted.ResultData)
	subscription = true
	c = seedanceContext(2)
	c.Request.Header.Set("Idempotency-Key", "no-entitlement")
	v, e := CreateSeedanceVerification(c, SeedanceVerificationInput{Model: "seedance-2.5"})
	require.NoError(t, e)
	require.Equal(t, "failed", v.Status)
	require.Equal(t, "entitlement_required", v.FailReason)
	again, e := CreateSeedanceVerification(c, SeedanceVerificationInput{Model: "seedance-2.5"})
	require.NoError(t, e)
	require.Equal(t, v.ID, again.ID)
	require.NoError(t, db.AutoMigrate(&model.SeedanceResource{}))
	require.NoError(t, db.AutoMigrate(&model.SeedanceResource{}))
}

func TestSeedanceOfficialAssetHostRecognition(t *testing.T) {
	for _, base := range []string{"https://ark.cn-beijing.volces.com", "https://ark.cn-beijing.volces.com/", "https://ark.cn-beijing.volces.com/api/v3"} {
		ch := &model.Channel{Type: constant.ChannelTypeDoubaoVideo, BaseURL: &base}
		require.True(t, isVolcSeedanceChannel(ch))
		require.True(t, isBytePlusSeedanceChannel(ch))
	}
	base := "https://ark.cn-beijing.volces.com.attacker.example"
	require.False(t, isVolcSeedanceChannel(&model.Channel{Type: constant.ChannelTypeDoubaoVideo, BaseURL: &base}))
}

func TestSeedanceAbandonedSessionCredentialsExpireWithoutPolling(t *testing.T) {
	db := seedanceTestDB(t)
	records := []model.SeedanceResource{
		{ID: "verification_expired", Kind: "verification", UserID: 1, Status: "pending", ExpiresAt: time.Now().Unix() - 1, UpstreamID: "secret-token", ResultData: "sensitive-h5"},
		{ID: "verification_completed", Kind: "verification", UserID: 1, Status: "completed", ExpiresAt: time.Now().Unix() - 1, GroupID: "group_owned"},
		{ID: "verification_pending", Kind: "verification", UserID: 1, Status: "pending", ExpiresAt: time.Now().Unix() + 1000, UpstreamID: "still-needed"},
	}
	require.NoError(t, db.Create(&records).Error)
	require.NoError(t, ExpireSeedanceVerificationSessions())
	require.NoError(t, ExpireSeedanceVerificationSessions())
	expired, e := model.GetSeedanceResource(1, "verification", records[0].ID)
	require.NoError(t, e)
	require.Equal(t, "expired", expired.Status)
	require.Empty(t, expired.UpstreamID)
	require.Empty(t, expired.ResultData)
	completed, e := model.GetSeedanceResource(1, "verification", records[1].ID)
	require.NoError(t, e)
	require.Equal(t, "completed", completed.Status)
	require.Equal(t, "group_owned", completed.GroupID)
	pending, e := model.GetSeedanceResource(1, "verification", records[2].ID)
	require.NoError(t, e)
	require.Equal(t, "still-needed", pending.UpstreamID)
}
