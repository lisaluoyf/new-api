package middleware

import (
	"bytes"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTaskWebhookNotificationsAllSeedanceAndMultipart(t *testing.T) {
	old := model.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	t.Cleanup(func() { model.DB = old })
	require.NoError(t, db.AutoMigrate(&model.TaskWebhookEndpoint{}))
	require.NoError(t, db.Create(&model.TaskWebhookEndpoint{ID: "ep", UserID: 7, Verified: true, Enabled: true}).Error)
	for _, name := range []string{"seedance-2.0", "seedance-2.5", "seedance-2.0-fast", "seedance-2.0-mini"} {
		t.Run(name, func(t *testing.T) {
			r := gin.New()
			r.Use(func(c *gin.Context) { c.Set("id", 7) })
			r.POST("/v1/videos/generations", TaskWebhookNotifications(), func(c *gin.Context) {
				config := service.TaskWebhookConfigFromContext(c)
				require.NotNil(t, config)
				require.Equal(t, "ep", config.EndpointID)
				b, _ := common.GetBodyStorage(c)
				raw, _ := b.Bytes()
				require.NotContains(t, string(raw), "notifications")
				require.NotContains(t, string(raw), "client_reference_id")
				require.Contains(t, string(raw), name)
				c.Status(200)
			})
			body := fmt.Sprintf(`{"model":%q,"prompt":"boat","notifications":{"webhook":{"endpoint_id":"ep"}},"client_reference_id":"order"}`, name)
			w := httptest.NewRecorder()
			q := httptest.NewRequest("POST", "/v1/videos/generations", strings.NewReader(body))
			q.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, q)
			require.Equal(t, 200, w.Code)
		})
	}
	t.Run("multipart", func(t *testing.T) {
		var b bytes.Buffer
		writer := multipart.NewWriter(&b)
		_ = writer.WriteField("model", "gpt-image-2")
		_ = writer.WriteField("notifications", `{"webhook":{"endpoint_id":"ep"}}`)
		f, _ := writer.CreateFormFile("image", "reference.png")
		_, _ = f.Write([]byte("unchanged-file"))
		_ = writer.Close()
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set("id", 7) })
		r.POST("/v1/images/edits/async", TaskWebhookNotifications(), func(c *gin.Context) {
			require.NotNil(t, service.TaskWebhookConfigFromContext(c))
			require.NoError(t, c.Request.ParseMultipartForm(1<<20))
			require.Empty(t, c.PostForm("notifications"))
			file, _, err := c.Request.FormFile("image")
			require.NoError(t, err)
			data, _ := io.ReadAll(file)
			file.Close()
			require.Equal(t, "unchanged-file", string(data))
			c.Status(http.StatusOK)
		})
		w := httptest.NewRecorder()
		q := httptest.NewRequest("POST", "/v1/images/edits/async", bytes.NewReader(b.Bytes()))
		q.Header.Set("Content-Type", writer.FormDataContentType())
		r.ServeHTTP(w, q)
		require.Equal(t, 200, w.Code)
	})
}
