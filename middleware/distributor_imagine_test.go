package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestImagineQueryUsesOwnedTaskModelForRestrictedTokens(t *testing.T) {
	i18n.Init()
	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.ImagineBatch{}, &model.ImagineTask{}))
	require.NoError(t, db.Create(&model.ImagineBatch{ID: "imagine_batch_test", UserID: 42, Model: "midjourney-niji-7"}).Error)
	require.NoError(t, db.Create(&model.ImagineTask{ID: "imagine_test", BatchID: "imagine_batch_test"}).Error)
	for _, tc := range []struct {
		path    string
		user    int
		allowed string
		status  int
	}{
		{"imagine_batch_test", 42, "midjourney-niji-7", 200},
		{"imagine_test", 42, "midjourney-niji-7", 200},
		{"imagine_test?model=gpt-image-2", 42, "midjourney-niji-7", 200},
		{"imagine_test?model=gpt-image-2", 42, "gpt-image-2", 403},
		{"imagine_test", 99, "midjourney-niji-7", 404},
		{"imagine_batch_test", 99, "midjourney-niji-7", 404},
		{"imagine_missing", 42, "midjourney-niji-7", 404},
	} {
		router := gin.New()
		router.Use(func(c *gin.Context) {
			c.Set("id", tc.user)
			common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, true)
			common.SetContextKey(c, constant.ContextKeyTokenModelLimit, map[string]bool{tc.allowed: true})
		})
		router.GET("/v1/tasks/:task_id", Distribute(), func(c *gin.Context) { c.Status(http.StatusOK) })
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/tasks/"+tc.path, nil))
		require.Equal(t, tc.status, w.Code, "%s user=%d: %s", tc.path, tc.user, w.Body.String())
	}
}

func TestVideoQueryUsesOwnedSavedModelForRestrictedTokens(t *testing.T) {
	i18n.Init()
	oldDB := model.DB
	t.Cleanup(func() { model.DB = oldDB })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Task{}))
	require.NoError(t, db.Create(&model.Task{TaskID: "task_owned", UserId: 42, ChannelId: 273, Properties: model.Properties{OriginModelName: "seedance-2.0"}}).Error)
	for _, tc := range []struct {
		path    string
		user    int
		allowed string
		status  int
	}{
		{"task_owned", 42, "seedance-2.0", 200},
		{"task_owned?model=seedance-2.5", 42, "seedance-2.0", 200},
		{"task_owned?model=seedance-2.5", 42, "seedance-2.5", 403},
		{"task_owned", 99, "seedance-2.0", 404},
		{"task_missing", 42, "seedance-2.0", 404},
	} {
		router := gin.New()
		router.Use(func(c *gin.Context) {
			c.Set("id", tc.user)
			common.SetContextKey(c, constant.ContextKeyTokenGroup, "default")
			common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, true)
			common.SetContextKey(c, constant.ContextKeyTokenModelLimit, map[string]bool{tc.allowed: true})
		})
		router.GET("/v1/videos/:video_id", Distribute(), func(c *gin.Context) { c.Status(200) })
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/v1/videos/"+tc.path, nil))
		require.Equal(t, tc.status, w.Code, "%s: %s", tc.path, w.Body.String())
	}
}
