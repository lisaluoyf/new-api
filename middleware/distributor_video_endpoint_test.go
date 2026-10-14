package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDistributeRejectsVideoChatBeforeChannelSelection(t *testing.T) {
	require.NoError(t, i18n.Init())
	oldDB, oldLogDB := model.DB, model.LOG_DB
	model.DB, model.LOG_DB = nil, nil
	t.Cleanup(func() { model.DB, model.LOG_DB = oldDB, oldLogDB })
	for _, path := range []string{"/v1/chat/completions", "/v1/completions", "/pg/chat/completions"} {
		for _, name := range []string{"seedance-2.0-mini", "seedance-2.0-fast", "seedance-2.0", "seedance-2.5", "doubao-seedance-2-0-mini-260615", "sora-2", "minimax-h3"} {
			t.Run(path+"/"+name, func(t *testing.T) {
				downstream := false
				router := gin.New()
				router.Use(BodyStorageCleanup(), func(c *gin.Context) {
					c.Set(string(constant.ContextKeyLanguage), i18n.LangEn)
					c.Set(common.RequestIdKey, "endpoint-guard-test")
					// An invalid pinned channel must not trigger a DB lookup.
					common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, "999")
				})
				router.POST(path, Distribute(), func(c *gin.Context) { downstream = true })
				body, err := common.Marshal(map[string]any{"model": name, "messages": []any{map[string]any{"role": "user", "content": "hi"}}, "stream": true})
				require.NoError(t, err)
				request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(body)))
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
				require.False(t, downstream, "rejected requests must not reach billing or fallback")
				var envelope struct {
					Error struct{ Code, Message string }
				}
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &envelope))
				require.Equal(t, "model_not_supported_on_endpoint", envelope.Error.Code)
				require.Contains(t, envelope.Error.Message, name)
				require.Contains(t, envelope.Error.Message, "POST /v1/videos/generations")
				require.Contains(t, envelope.Error.Message, "endpoint-guard-test")
			})
		}
	}
}

func TestVideoEndpointGuardAllowsVideoAndTextRoutes(t *testing.T) {
	for _, tc := range []struct{ model, path string }{
		{"seedance-2.0-mini", "/v1/videos/generations"},
		{"seedance-2.0-mini", "/v1/videos"},
		{"seedance-2.0-mini", "/v1/videos/task_test"},
		{"gpt-6.1-sol", "/v1/chat/completions"},
		{"minimax-text-01", "/v1/chat/completions"},
		{"gemini-3.1-flash-image", "/v1/chat/completions"},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, tc.path, nil)
		require.False(t, rejectVideoModelOnTextEndpoint(c, tc.model), "%s %s", tc.model, tc.path)
		require.False(t, c.IsAborted())
	}
}
