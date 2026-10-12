package controller

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type seedancePreviewTransport func(*http.Request) (*http.Response, error)

func (f seedancePreviewTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSeedanceAssetPreviewOwnershipRefreshAndNoSourceFallback(t *testing.T) {
	oldDB, oldMemory, oldTransport := model.DB, common.MemoryCacheEnabled, http.DefaultTransport
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.SeedanceResource{}, &model.Channel{}))
	model.DB, common.MemoryCacheEnabled = db, false
	fetch := system_setting.GetFetchSetting()
	oldProtection := fetch.EnableSSRFProtection
	fetch.EnableSSRFProtection = false
	t.Cleanup(func() {
		model.DB, common.MemoryCacheEnabled, http.DefaultTransport = oldDB, oldMemory, oldTransport
		fetch.EnableSSRFProtection = oldProtection
	})
	base := "https://api.xingchiyun.com/v1/seedance"
	require.NoError(t, db.Create(&model.Channel{Id: 292, Type: constant.ChannelTypeDoubaoVideo, Status: 1, Key: "private-key", BaseURL: &base}).Error)
	asset := model.SeedanceResource{ID: "asset_public", UserID: 1, Kind: "asset", ChannelID: 292, Model: "seedance-2.5", AssetType: "Image", KeyFingerprint: service.SeedanceKeyFingerprint("private-key"), UpstreamID: "asset-private", Status: "Active", SourceURL: "https://input.example/original.jpg"}
	require.NoError(t, db.Create(&asset).Error)
	hits, available, html := 0, true, false
	http.DefaultTransport = seedancePreviewTransport(func(r *http.Request) (*http.Response, error) {
		hits++
		body, contentType := "image fixture", "image/jpeg"
		if r.Method == "POST" {
			require.Equal(t, "api.xingchiyun.com", r.URL.Host)
			require.Equal(t, "GetAsset", r.URL.Query().Get("Action"))
			require.Equal(t, "Bearer private-key", r.Header.Get("Authorization"))
			body = `{"Result":{"Status":"Active"}}`
			if available {
				body = `{"Result":{"Status":"Active","URL":"https://media.example/preview.jpg?X-Tos-Signature=secret"}}`
			}
			contentType = "application/json"
		} else {
			require.Equal(t, "media.example", r.URL.Host)
			require.Equal(t, "secret", r.URL.Query().Get("X-Tos-Signature"))
			require.Empty(t, r.Header.Get("Authorization"))
			if html {
				contentType = "text/html"
			}
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	invoke := func(user int) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/preview", nil)
		c.Set("id", user)
		c.Params = gin.Params{{Key: "resource_id", Value: asset.ID}}
		PreviewSeedanceAsset(c)
		return w
	}
	require.Equal(t, 404, invoke(2).Code)
	require.Zero(t, hits)
	w := invoke(1)
	require.Equal(t, 200, w.Code)
	require.Equal(t, "image fixture", w.Body.String())
	require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
	require.Equal(t, 2, hits)
	available = false
	require.Equal(t, 404, invoke(1).Code)
	require.Equal(t, 3, hits)
	available, html = true, true
	require.Equal(t, 502, invoke(1).Code)
	var stored model.SeedanceResource
	require.NoError(t, db.First(&stored, "id = ?", asset.ID).Error)
	require.Equal(t, "Active", stored.Status)
}
