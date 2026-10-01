package controller

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSeedanceControllersNeverExposeAnotherUsersLibrary(t *testing.T) {
	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.SeedanceResource{}))
	require.NoError(t, db.Create(&[]model.SeedanceResource{
		{ID: "asset_foreign", Kind: "asset", UserID: 2, UpstreamID: "private-asset", Status: "Active"},
		{ID: "group_foreign", Kind: "group", UserID: 2, UpstreamID: "private-group", Status: "Active"},
		{ID: "asset_task_foreign", Kind: "task", UserID: 2, UpstreamID: "private-review", Status: "completed", ResultData: `{"private":"forbidden"}`},
	}).Error)
	for _, kind := range []string{"asset", "group"} {
		for _, handler := range []gin.HandlerFunc{GetSeedanceResource, UpdateSeedanceResource, DeleteSeedanceResource} {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Set("id", 1)
			c.Set("seedance_resource_kind", kind)
			c.Params = gin.Params{{Key: "resource_id", Value: kind + "_foreign"}}
			c.Request = httptest.NewRequest("PATCH", "/", strings.NewReader(`{"name":"hacked"}`))
			handler(c)
			require.Equal(t, 404, w.Code)
			require.NotContains(t, w.Body.String(), "private-")
		}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("id", 1)
		c.Set("seedance_resource_kind", kind)
		c.Request = httptest.NewRequest("GET", "/", nil)
		ListSeedanceResources(c)
		require.Equal(t, 200, w.Code)
		require.Contains(t, w.Body.String(), `"items":[]`)
		require.Contains(t, w.Body.String(), `"total":0`)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("id", 1)
	c.Params = gin.Params{{Key: "task_id", Value: "asset_task_foreign"}}
	c.Request = httptest.NewRequest("GET", "/v1/tasks/asset_task_foreign", nil)
	FetchSeedanceAssetTask(c)
	require.Equal(t, 404, w.Code)
	require.NotContains(t, w.Body.String(), "forbidden")
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Set("id", 1)
	c.Params = gin.Params{{Key: "task_id", Value: "private-review"}}
	c.Request = httptest.NewRequest("GET", "/v1/tasks/private-review", nil)
	RelayImageTask(c)
	require.Equal(t, 404, w.Code)
	require.NotContains(t, w.Body.String(), "forbidden")
}
