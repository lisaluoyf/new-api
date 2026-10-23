package service

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func beeNexReliabilityFixture(t *testing.T) (*gorm.DB, *gin.Context, beeNexAssetCredentials) {
	db := seedanceTestDB(t, filepath.Join(t.TempDir(), "bee.sqlite"))
	require.NoError(t, db.AutoMigrate(&model.User{}))
	require.NoError(t, db.Create(&model.User{Id: 1, Role: 100}).Error)
	dir := t.TempDir()
	t.Setenv("BEENEX_ASSET_CREDENTIALS_DIR", dir)
	raw, err := common.Marshal(map[string]any{"access_key_id": "test-ak", "secret_access_key": "test-sk", "region": "cn-beijing", "project_mode": "account_scoped", "account_scoped_projects": []string{"ZCH"}, "official_h5_enabled": true})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "channel-274.json"), raw, 0600))
	base := "https://beenexapi.phone580.com"
	require.NoError(t, db.Create(&model.Channel{Id: 274, Type: 1, Status: 1, Key: "test-video-key", BaseURL: &base, Models: "seedance-2.0,seedance-2.5", Group: "default"}).Error)
	for _, name := range []string{"seedance-2.0", "seedance-2.5"} {
		require.NoError(t, db.Create(&model.Ability{ChannelId: 274, Model: name, Group: "default", Enabled: true}).Error)
	}
	model.InitChannelCache()
	c := seedanceContext(1)
	common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, "274")
	credentials, err := loadBeeNexAssetCredentials(274)
	require.NoError(t, err)
	return db, c, credentials
}

func TestBeeNexUnknownAssetRecoveryQueriesOnly(t *testing.T) {
	for _, scenario := range []string{"match", "duplicates", "wrong_group", "wrong_identity", "deleted"} {
		t.Run(scenario, func(t *testing.T) {
			db, c, credentials := beeNexReliabilityFixture(t)
			fp := beeNexControlFingerprint(credentials)
			keyfp := SeedanceKeyFingerprint("test-video-key")
			group := model.SeedanceResource{ID: "group-local", UserID: 1, Kind: "group", ChannelID: 274, KeyFingerprint: keyfp, CredentialFingerprint: fp, UpstreamID: "group-native", GroupType: "virtual", Status: "Active"}
			asset := model.SeedanceResource{ID: "asset-local", UserID: 1, Kind: "asset", ChannelID: 274, KeyFingerprint: keyfp, CredentialFingerprint: fp, GroupID: group.ID, AssetType: "Image", Status: "submission_unknown", Model: "seedance-2.5"}
			require.NoError(t, db.Create(&group).Error)
			require.NoError(t, db.Create(&asset).Error)
			calls := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				action := r.URL.Query().Get("Action")
				calls = append(calls, action)
				w.Header().Set("Content-Type", "application/json")
				switch action {
				case "ListAssets":
					groupID := "group-native"
					if scenario == "wrong_group" {
						groupID = "another-group"
					}
					item := fmt.Sprintf(`{"Id":"asset-native","Name":"asset-local","GroupId":%q,"AssetType":"Image","Status":"Unavailable","ProjectName":"ZCH"}`, groupID)
					if scenario == "duplicates" {
						fmt.Fprintf(w, `{"Result":{"TotalCount":2,"Items":[%s,%s]}}`, item, item)
					} else {
						fmt.Fprintf(w, `{"Result":{"TotalCount":1,"Items":[%s]}}`, item)
					}
				case "GetAsset":
					name := "asset-local"
					if scenario == "wrong_identity" {
						name = "different-asset"
					}
					if scenario == "deleted" {
						require.NoError(t, db.Delete(&asset).Error)
					}
					fmt.Fprintf(w, `{"Result":{"Id":"asset-native","Name":%q,"GroupId":"group-native","AssetType":"Image","Status":"Active","ProjectName":"ZCH","URL":"https://media.example/preview.jpg","Error":{"Code":"","Message":""}}}`, name)
				default:
					t.Errorf("Unexpected mutation %s", action)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			previous := beeNexAssetEndpoint
			beeNexAssetEndpoint = func(*model.Channel) string { return server.URL + "/post-proxy/" }
			t.Cleanup(func() { beeNexAssetEndpoint = previous })
			err := RefreshSeedanceAsset(c, &asset)
			if scenario == "match" {
				require.NoError(t, err)
				require.Equal(t, "asset-native", asset.UpstreamID)
				require.Equal(t, "Active", asset.Status)
			} else {
				require.Error(t, err)
			}
			for _, action := range calls {
				require.Contains(t, []string{"ListAssets", "GetAsset"}, action)
			}
			if scenario == "deleted" {
				var count int64
				require.NoError(t, db.Model(&model.SeedanceResource{}).Where("id = ?", "asset-local").Count(&count).Error)
				require.Zero(t, count)
			}
		})
	}
}

func TestBeeNexOfficialH5Contract(t *testing.T) {
	_, c, _ := beeNexReliabilityFixture(t)
	c.Request.Header.Set("Idempotency-Key", "bee-h5-test-key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var fields map[string]any
		require.NoError(t, common.DecodeJson(r.Body, &fields))
		switch r.URL.Query().Get("Action") {
		case "CreateVisualValidateSession":
			require.Empty(t, fields)
			fmt.Fprint(w, `{"Result":{"BytedToken":"test-private-h5-token","H5Link":"https://verify.example/owner?token=private"}}`)
		case "GetVisualValidateResult":
			require.Equal(t, "test-private-h5-token", fields["BytedToken"])
			fmt.Fprint(w, `{"Result":{"GroupId":null}}`)
		default:
			t.Fatal("Unexpected H5 operation")
		}
	}))
	defer server.Close()
	previous := beeNexAssetEndpoint
	beeNexAssetEndpoint = func(*model.Channel) string { return server.URL + "/post-proxy/" }
	t.Cleanup(func() { beeNexAssetEndpoint = previous })
	resource, err := CreateSeedanceVerification(c, SeedanceVerificationInput{Model: "seedance-2.5", ChannelID: 274})
	require.NoError(t, err)
	require.Equal(t, "pending", resource.Status)
	require.NoError(t, RefreshSeedanceVerification(c, resource))
	require.Equal(t, "pending", resource.Status)
	require.Empty(t, resource.GroupID)
	dto := SeedanceVerificationDTO(resource)
	require.Equal(t, "pending", dto["status"])
	require.Contains(t, dto, "verification_url")
	require.NotContains(t, dto, "BytedToken")
}
