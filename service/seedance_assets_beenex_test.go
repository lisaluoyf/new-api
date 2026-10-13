package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestBeeNexAssetSignedProxyAndOwnedLifecycle(t *testing.T) {
	db := seedanceTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}))
	require.NoError(t, db.Create(&model.User{Id: 1, Role: 100}).Error)
	dir := t.TempDir()
	t.Setenv("BEENEX_ASSET_CREDENTIALS_DIR", dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "channel-274.json"), []byte(`{"access_key_id":"beenex-ak","secret_access_key":"beenex-sk","region":"cn-beijing","project_name":"beenex-project"}`), 0600))
	base := "https://beenexapi.phone580.com"
	require.NoError(t, db.Create(&model.Channel{Id: 274, Type: 1, Status: 1, Key: "beenex-inference-key", BaseURL: &base, Models: "seedance-2.5", Group: "default"}).Error)
	require.NoError(t, db.Create(&model.Ability{ChannelId: 274, Model: "seedance-2.5", Group: "default", Enabled: false}).Error)
	model.InitChannelCache()
	calls := []string{}
	phase := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/post-proxy/", r.URL.Path)
		require.Equal(t, "POST", r.Method)
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, fmt.Sprintf("%x", sha256.Sum256(raw)), r.Header.Get("X-Content-Sha256"))
		// Independent reference from the supplied Apifox pre-request script: its
		// canonical URI is '/', including the actual host and non-default port.
		date := r.Header.Get("X-Date")
		scope := date[:8] + "/cn-beijing/ark/request"
		signedHeaders := "content-type;host;x-content-sha256;x-date"
		hash := fmt.Sprintf("%x", sha256.Sum256(raw))
		canonicalHeaders := "content-type:application/json\nhost:" + r.Host + "\nx-content-sha256:" + hash + "\nx-date:" + date + "\n"
		canonical := "POST\n/\n" + r.URL.RawQuery + "\n" + canonicalHeaders + "\n" + signedHeaders + "\n" + hash
		stringToSign := "HMAC-SHA256\n" + date + "\n" + scope + "\n" + fmt.Sprintf("%x", sha256.Sum256([]byte(canonical)))
		mac := func(key []byte, data string) []byte {
			h := hmac.New(sha256.New, key)
			h.Write([]byte(data))
			return h.Sum(nil)
		}
		key := mac([]byte("beenex-sk"), date[:8])
		for _, field := range []string{"cn-beijing", "ark", "request"} {
			key = mac(key, field)
		}
		expected := "HMAC-SHA256 Credential=beenex-ak/" + scope + ", SignedHeaders=" + signedHeaders + ", Signature=" + fmt.Sprintf("%x", mac(key, stringToSign))
		require.True(t, hmac.Equal([]byte(expected), []byte(r.Header.Get("Authorization"))))
		require.NotContains(t, r.Header.Get("Authorization"), "beenex-inference-key")
		var fields map[string]any
		require.NoError(t, common.Unmarshal(raw, &fields))
		require.NotContains(t, fields, "ProjectName")
		action := r.URL.Query().Get("Action")
		calls = append(calls, action)
		switch action {
		case "CreateAssetGroup":
			require.Equal(t, "AIGC", fields["GroupType"])
			fmt.Fprint(w, `{"Result":{"Id":"bee-group"}}`)
		case "CreateAsset":
			require.Equal(t, "bee-group", fields["GroupId"])
			fmt.Fprint(w, `{"Result":{"Id":"bee-asset"}}`)
		case "GetAsset":
			status := "Processing"
			if phase > 0 {
				status = "Active"
			}
			project := "beenex-project"
			if phase == 2 {
				project = "different-project"
			}
			fmt.Fprintf(w, `{"Result":{"Id":"bee-asset","Status":%q,"ProjectName":%q}}`, status, project)
		case "ListAssets":
			fmt.Fprint(w, `{"Result":{"Items":[{"Id":"bee-asset","Status":"Active","ProjectName":"beenex-project"}]}}`)
		default:
			t.Errorf("unexpected action %s", action)
			w.WriteHeader(500)
		}
	}))
	defer upstream.Close()
	old := beeNexAssetEndpoint
	beeNexAssetEndpoint = func(*model.Channel) string { return upstream.URL + "/post-proxy/" }
	t.Cleanup(func() { beeNexAssetEndpoint = old })
	c := seedanceContext(1)
	common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, "274")
	caps, err := SeedancePortraitCapabilities(c, "seedance-2.5")
	require.NoError(t, err)
	require.Len(t, caps, 1)
	require.Equal(t, true, caps[0]["configured"])
	require.Equal(t, false, caps[0]["real_person_verified"])
	task, err := SubmitSeedanceAssets(c, SeedanceAssetSubmission{Model: "seedance-2.5", Group: &SeedanceGroupInput{ChannelID: 274, Name: "Bee group", Purpose: "ordinary"}, Assets: []SeedanceAssetInput{{URL: "https://example.com/synthetic.png", Name: "Synthetic"}}})
	require.NoError(t, err)
	require.Equal(t, 274, task.ChannelID)
	require.Equal(t, "beenex-project", task.ProjectName)
	require.NoError(t, PollSeedanceAssetTask(c, task))
	require.Equal(t, "processing", task.Status)
	phase = 2
	require.Error(t, PollSeedanceAssetTask(c, task))
	var unverified model.SeedanceResource
	require.NoError(t, db.First(&unverified, "channel_id = ? AND kind = ?", 274, "asset").Error)
	require.Equal(t, "Processing", unverified.Status)
	phase = 1
	require.NoError(t, PollSeedanceAssetTask(c, task))
	require.Equal(t, "completed", task.Status)
	var assets []model.SeedanceResource
	require.NoError(t, db.Where("channel_id = ? AND kind = ?", 274, "asset").Find(&assets).Error)
	require.Len(t, assets, 1)
	asset := assets[0]
	require.Equal(t, "Active", asset.Status)
	require.NotEqual(t, "real_person", asset.GroupType)
	fields := map[string]any{"model": "seedance-2.5", "image_urls": []any{"asset://" + asset.ID}, "seed": float64(0)}
	pinned, err := SeedanceAssetRouting(c, fields)
	require.NoError(t, err)
	require.Equal(t, 274, pinned.ChannelID)
	out, err := ResolveSeedanceAssetReferences(c, fields, 274, "beenex-inference-key")
	require.NoError(t, err)
	require.Equal(t, []any{"asset://bee-asset"}, out["image_urls"])
	require.Equal(t, float64(0), out["seed"])
	_, err = ResolveSeedanceAssetReferences(c, fields, 273, "another-key")
	require.Error(t, err)
	_, err = beeNexAssetRequest(c.Request.Context(), &asset, "ListAssets", map[string]any{"Filter": map[string]any{"GroupIds": []string{"bee-group"}, "GroupType": "AIGC"}, "PageNumber": 1, "PageSize": 10})
	require.NoError(t, err)
	encoded, err := common.Marshal(SeedanceAssetDTO(&asset))
	require.NoError(t, err)
	for _, secret := range []string{"beenex-ak", "beenex-sk", "bee-group", "bee-asset", "beenex-inference-key"} {
		require.NotContains(t, string(encoded), secret)
	}
	// No distribution or credential context is changed by material operations.
	var ability model.Ability
	require.NoError(t, db.First(&ability, "channel_id = ?", 274).Error)
	require.False(t, ability.Enabled)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "channel-274.json"), []byte(`{"access_key_id":"other-account","secret_access_key":"beenex-sk","region":"cn-beijing","project_name":"other-project"}`), 0600))
	_, err = SeedanceAssetRouting(c, fields)
	require.Error(t, err)
	require.Contains(t, calls, "CreateAssetGroup")
	require.Contains(t, calls, "CreateAsset")
	require.Contains(t, calls, "GetAsset")
	require.Contains(t, calls, "ListAssets")
}

func TestBeeNexMissingCredentialsDoNotUseOtherChannel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BEENEX_ASSET_CREDENTIALS_DIR", dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "channel-273.json"), []byte(`{"access_key_id":"wrong","secret_access_key":"wrong","region":"cn-beijing","project_name":"wrong"}`), 0600))
	_, err := loadBeeNexAssetCredentials(274)
	require.Error(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "channel-274.json"), []byte(`{"access_key_id":"ak","secret_access_key":"sk","region":"cn-beijing","project_name":"project"}`), 0644))
	_, err = loadBeeNexAssetCredentials(274)
	require.Error(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "channel-274.json"), []byte(`{"access_key_id":"ak","secret_access_key":"sk","region":"ap-southeast-1","project_name":"project"}`), 0600))
	require.NoError(t, os.Chmod(filepath.Join(dir, "channel-274.json"), 0600))
	_, err = loadBeeNexAssetCredentials(274)
	require.ErrorContains(t, err, "cn-beijing")
}
