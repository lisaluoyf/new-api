package service

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestTokenHubExplicitMaterialModerationWireAndCapabilities(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "default_skip", true: "explicit_skip"}[explicit], func(t *testing.T) {
			tokenHubTestCredentials(t)
			db := seedanceTestDB(t, filepath.Join(t.TempDir(), "library.db"))
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(2)
			require.NoError(t, db.AutoMigrate(&model.ChannelModelPricing{}))
			base, settings := "https://ai-tokenhub.ai", `{"manual_group_ratio":1}`
			require.NoError(t, db.Create(&model.Channel{Id: 297, Type: 54, Key: "test-video-key", BaseURL: &base, Setting: &settings, Status: 1, Models: "seedance-2.5", Group: "default"}).Error)
			require.NoError(t, db.Create(&model.Ability{ChannelId: 297, Model: "seedance-2.5", Group: "default", Enabled: true}).Error)
			model.InitChannelCache()
			old := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = old })
			sends := 0
			http.DefaultTransport = automaticTokenHubTransport(func(r *http.Request) (*http.Response, error) {
				require.Equal(t, "ai-tokenhub.ai", r.URL.Host)
				require.Equal(t, "/ark", r.URL.Path)
				var fields map[string]any
				require.NoError(t, common.DecodeJson(r.Body, &fields))
				body := ""
				switch r.URL.Query().Get("Action") {
				case "CreateAssetGroup":
					require.NotContains(t, fields, "Moderation")
					body = `{"Result":{"Id":"upstream-group"}}`
				case "CreateAsset":
					sends++
					require.Equal(t, map[string]any{"Strategy": "Skip"}, fields["Moderation"])
					body = `{"Result":{"Id":"upstream-asset"}}`
				case "GetAsset":
					body = `{"Result":{"Id":"upstream-asset","Status":"Active"}}`
				default:
					t.Fatalf("unexpected control operation %s", r.URL.RawQuery)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})
			c := seedanceContext(1)
			c.Request.Header.Set("Idempotency-Key", "tokenhub-moderation-wire")
			var input SeedanceAssetSubmission
			require.NoError(t, common.UnmarshalJsonStr(`{"model":"seedance-2.5","group":{"channel_id":297},"assets":[{"url":"https://example.com/synthetic.png"}],"Moderation":{"Strategy":"Skip"}}`, &input))
			if !explicit {
				input.Moderation = nil
			}
			task, err := SubmitSeedanceAssets(c, input)
			require.NoError(t, err)
			require.NoError(t, PollSeedanceAssetTask(c, task))
			var asset model.SeedanceResource
			require.NoError(t, db.Where("kind = ?", "asset").First(&asset).Error)
			require.Equal(t, "Active", asset.Status)
			moderation := SeedanceAssetDTO(&asset)["moderation"].(map[string]any)
			require.Equal(t, "Skip", moderation["requested"].(*SeedanceModeration).Strategy)
			require.Equal(t, true, moderation["forwarded"])
			require.Equal(t, false, moderation["provider_effect_confirmed"])
			// Replaying the same key does not submit another material.
			_, err = SubmitSeedanceAssets(c, input)
			require.NoError(t, err)
			require.Equal(t, 1, sends)
			if explicit {
				input.Moderation = nil
			} else {
				input.Moderation = &SeedanceModeration{Strategy: "Skip"}
			}
			_, err = SubmitSeedanceAssets(c, input)
			require.ErrorContains(t, err, "different request")
			caps, err := SeedancePortraitCapabilities(c, "seedance-2.5")
			require.NoError(t, err)
			require.Len(t, caps, 1)
			require.Equal(t, true, caps[0]["configured"])
			require.Equal(t, "virtual_or_ordinary", caps[0]["method"])
			require.Equal(t, false, caps[0]["real_person_verified"])
			require.Equal(t, false, caps[0]["hosted_callback"])
			require.Equal(t, "ordinary_asset_lifecycle_observed", caps[0]["live_acceptance"])
		})
	}

}

func TestSeedanceModerationRejectsUnsupportedPolicy(t *testing.T) {
	for _, value := range []any{nil, "Skip", map[string]any{"Strategy": "Other"}, map[string]any{}} {
		_, err := ParseSeedanceModeration(value)
		require.Error(t, err)
	}
}
