package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRefreshModelPricingRestrictsExplicitChannelScope(t *testing.T) {
	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	// An in-memory SQLite database belongs to one connection. Both async
	// refreshes must share the migrated fixture instead of opening empty DBs.
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.ChannelModelPricing{}))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"group_ratio":{"pro":1},"data":[{"model_name":"refresh-model","model_ratio":1,"completion_ratio":2}]}`))
	}))
	defer server.Close()
	setting, base := `{"key_group":"pro"}`, server.URL
	for _, id := range []int{15, 16, 17} {
		require.NoError(t, db.Create(&model.Channel{Id: id, Status: 1, Models: "refresh-model", BaseURL: &base, Setting: &setting}).Error)
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/admin/channel-data/refresh-pricing", strings.NewReader(`{"channel_ids":[15,16]}`))
	RefreshModelPricing(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)
	var result struct {
		Success bool `json:"success"`
		Count   int  `json:"count"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &result))
	require.True(t, result.Success)
	require.Equal(t, 2, result.Count)
	require.Eventually(t, func() bool {
		var count int64
		return db.Model(&model.ChannelModelPricing{}).Count(&count).Error == nil && count == 2
	}, 5*time.Second, 20*time.Millisecond)
	var rows []model.ChannelModelPricing
	require.NoError(t, db.Order("channel_id").Find(&rows).Error)
	require.Equal(t, 15, rows[0].ChannelId)
	require.Equal(t, 16, rows[1].ChannelId)
	// Invalid IDs cannot accidentally turn a scoped refresh into an all-channel one.
	recorder = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/admin/channel-data/refresh-pricing", strings.NewReader(`{"channel_ids":[0]}`))
	RefreshModelPricing(ctx)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}
